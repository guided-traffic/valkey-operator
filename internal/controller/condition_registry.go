package controller

import vkov1 "github.com/guided-traffic/valkey-operator/api/v1"

// This file declares, per status condition type, who owns it and what its lifecycle is.
// It changes no behaviour: nothing in the reconcile path reads it. Its only consumer is
// TestConditionRegistryCoversEveryConditionType and its siblings
// (condition_registry_test.go), which is the point.
//
// It exists because one failure shape has now been fixed four times at four different
// sites: a condition whose clear sits behind the very code path whose absence caused the
// staleness. ADR 0002 D10 fixed it for SidecarUpdatePending, ADR 0024 D6 for
// SentinelUpdatePending, ADR 0026 D5 for PodTerminationStalled, and ADR 0002 D10 again in
// 2026-08-26 for the completing pass. Two more instances (RollingUpdatePaused,
// StorageSpecNotApplied) were found by writing this table down rather than by an
// incident, and are recorded below as declared gaps rather than silently carried.
//
// The registry is the ADR 0014 idiom applied to conditions: a convention that has been
// missed this often is encoded as a test, not restated. Adding a condition type without a
// line here fails the build's unit tier, and a line that claims an edge has a clear it
// does not have fails too.
// See docs/adr/0027-conditions-are-levels-edges-or-history.md.

// conditionKind classifies what a condition's value means over time, which is what decides
// whether it needs a clear site at all.
type conditionKind string

const (
	// conditionLevel is re-measured from live state on every pass that reaches its
	// evaluator, so it corrects itself and needs no clear site. Its hazard is the
	// opposite one: an evaluator that is not reached leaves the last measurement
	// standing, and two evaluators for one level race to be last.
	conditionLevel conditionKind = "level"

	// conditionEdge records that something happened or that work is deferred. Nothing
	// re-measures it, so it needs a site that provably knows the precondition is gone
	// and a presence guard so clearing it does not stamp the condition onto CRs that
	// never had it.
	conditionEdge conditionKind = "edge"

	// conditionHistory is a verdict about a completed operation. It is deliberately
	// never cleared: the record outliving the state it describes is the feature, and
	// overwriting it discards the only durable trace of what happened.
	conditionHistory conditionKind = "history"
)

// conditionOwnership is one row of the registry.
type conditionOwnership struct {
	// conditionType is the value stored in status.conditions[].type.
	conditionType vkov1.ConditionType

	// kind decides which of the invariants below apply to this row.
	kind conditionKind

	// evaluators is how many independent call sites compute this condition's value.
	// Meaningful for a level: more than one and the last writer of a pass wins, which is
	// a race unless an ownership rule says which one is authoritative.
	evaluators int

	// ownershipRule is that rule, in prose, and it is what makes more than one
	// evaluator legal. It has to name which site decides, not merely assert that one
	// does — the reader next to the failure message is deciding whether a new call site
	// may be added. Empty is the only legal value at a single evaluator.
	ownershipRule string

	// clearSite names the function that flips the condition to False, empty for history
	// and for a declared gap. Prose, not a symbol: it is read by a human next to the
	// failure message, and a symbol reference would not survive a rename any better than
	// the ADR text does.
	clearSite string

	// presenceGuarded records whether the clear returns early on a CR that does not carry
	// the condition. Without it, the first upgraded pass writes the condition onto every
	// CR in the fleet (ADR 0005 D10).
	presenceGuarded bool

	// loadBearingField names a field of the stored condition that something else reads as
	// data rather than as a report, so rewriting the condition destroys it. Empty when
	// only Status and Reason matter.
	loadBearingField string

	// declaredGap is the ticket reference for a row that knowingly violates the invariant
	// its kind implies. A gap without a reference fails the test: an exception has to be
	// traceable to a decision, or it is just a broken invariant with a comment.
	declaredGap string
}

// conditionRegistry is the full set. Every ConditionType declared in api/v1 appears
// exactly once; the test enforces that.
var conditionRegistry = []conditionOwnership{
	{
		conditionType: vkov1.ConditionTypeReady,
		kind:          conditionLevel,
		// updateStandaloneStatus and updateHAStatus are the two arms of one dispatch, not
		// two independent evaluators: updateStatus picks exactly one per pass.
		evaluators: 1,
		clearSite:  "updateStandaloneStatus / updateHAStatus recompute it every pass",
		// A level needs no presence guard: it is written on the first pass either way.
		presenceGuarded: false,
		declaredGap:     "T18: a pass with a rolling update in flight returns before updateStatus, so Ready keeps its pre-roll value for the whole roll (ADR 0001 D4 decides this; re-decision open)",
	},
	{
		conditionType:   vkov1.ConditionTypeReconcileBlocked,
		kind:            conditionLevel,
		evaluators:      1,
		clearSite:       "setReconcileBlockedCondition, evaluated unconditionally every pass",
		presenceGuarded: true,
	},
	{
		conditionType:   vkov1.ConditionTypeSentinelPeersStale,
		kind:            conditionLevel,
		evaluators:      1,
		clearSite:       "recordSentinelPeerDrift",
		presenceGuarded: false,
	},
	{
		conditionType: vkov1.ConditionTypeStorageSpecNotApplied,
		kind:          conditionLevel,
		// The data StatefulSet and the Sentinel StatefulSet both call
		// guardVolumeClaimTemplates, so two sites still compute a value and the last
		// writer still owns Reason and Message. What is no longer a race is the one
		// that mattered: the Sentinel builder writes no claims, so its evaluator can
		// only ever agree, and agreement from a tier that has nothing to compare is
		// not evidence about the tier that does.
		evaluators:      2,
		ownershipRule:   "either tier may report a conflict; only the data tier may clear (mayClear), and only because it runs first",
		clearSite:       "guardVolumeClaimTemplates default arm, data tier only",
		presenceGuarded: true,
	},
	{
		conditionType:   vkov1.ConditionTypeSentinelUpdatePending,
		kind:            conditionLevel,
		evaluators:      1,
		clearSite:       "finishSentinelRollingUpdate, plus clearSentinelUpdatePending on class exit",
		presenceGuarded: true,
	},
	{
		conditionType: vkov1.ConditionTypeMultipleMasters,
		kind:          conditionLevel,
		evaluators:    1,
		clearSite:     "clearMultipleMasters",
		// The deadline splitBrainWarnAfter is measured from the stored
		// LastTransitionTime, and meta.SetStatusCondition only moves it on a status flip.
		// Anything that rewrites this condition to restate the same status resets the
		// deadline and the Warning never fires (ADR 0025).
		presenceGuarded:  true,
		loadBearingField: "LastTransitionTime",
	},
	{
		conditionType:   vkov1.ConditionTypeSidecarUpdatePending,
		kind:            conditionEdge,
		evaluators:      1,
		clearSite:       "clearSidecarUpdatePending, from the converged early return and from the completion branch of checkAndHandleRollingUpdate",
		presenceGuarded: true,
	},
	{
		conditionType:   vkov1.ConditionTypePodTerminationStalled,
		kind:            conditionEdge,
		evaluators:      1,
		clearSite:       "clearPodTerminationStalled, from the delete gate and from clearRollingUpdateState",
		presenceGuarded: true,
	},
	{
		conditionType: vkov1.ConditionTypeRollingUpdatePaused,
		kind:          conditionEdge,
		evaluators:    1,
		// Two sites, one frame above the dispatch, so every topology reaches them --
		// the clear used to sit inside finalizeRollingUpdate, which only the Sentinel
		// arm calls. The converged one is additionally gated on tier convergence: a
		// pod that does not exist is skipped by the ordinal loop and a replacement is
		// up to date the instant it appears, so "no pod needs updating" is not "the
		// tier converged" (ADR 0002 D10b).
		clearSite:       "clearRollingUpdatePaused, from the converged early return (tier-converged) and from the completion branch of checkAndHandleRollingUpdate",
		presenceGuarded: true,
	},
	{
		conditionType: vkov1.ConditionTypeTLSMaterialStale,
		kind:          conditionLevel,
		// One evaluator, and it is a reconcile step rather than a workload one on
		// purpose: runReconcileSteps runs every step of a pass, while every arm of
		// reconcileWorkload returns early while a rolling update is in flight --
		// which is precisely when this condition is True.
		evaluators: 1,
		clearSite:  "reportTLSMaterialStale, evaluated on every pass of a TLS cluster",
		// A level needs no presence guard, and here the gate does the same job from
		// the other side: the step carries when: IsTLSEnabled, so a cluster without
		// TLS never gains the condition at all.
		presenceGuarded: false,
	},
	{
		conditionType: vkov1.ConditionTypeTopologyRestored,
		kind:          conditionHistory,
		evaluators:    1,
		// Deliberately none. See the type comment and ADR 0010 D15: the verdict of the
		// last data-tier roll is meant to outlive the state it describes, and
		// status.masterPod is the live answer.
		clearSite:       "",
		presenceGuarded: false,
		// The abandon verdict is the only durable record that the cluster ran out of
		// budget waiting for pod-0, and the message names the pod that stayed master.
		loadBearingField: "Message",
	},
}
