package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// The guard behind conditionRegistry (condition_registry.go).
//
// Why a test and not a convention: the "a condition whose clear sits behind the code path
// whose absence caused the staleness" shape has been fixed four times at four sites, and
// two further instances were found by writing the table down. A convention that has been
// missed this often is encoded, not restated - the same argument ADR 0014 makes for the
// RBAC drift guard.
//
// What this can and cannot catch. It catches a new condition type with no declared owner,
// an edge with no clear site, an unguarded clear that would stamp the condition onto every
// CR in the fleet, a level with racing evaluators, and a history row that grew a clear. It
// cannot catch a row that is classified wrongly - a level declared as history still
// compiles and still passes. That half stays a review question.

// conditionTypesFile is the single place condition types are declared. Parsed rather than
// reflected over, because ConditionType is a type ALIAS for string
// (api/v1/valkey_types.go), so there is no distinct runtime type to enumerate and no
// reflection that could tell a condition type from any other string constant.
const conditionTypesFile = "api/v1/valkey_types.go"

// declaredConditionTypes returns every `ConditionType<Name> ConditionType = "<value>"`
// constant declared in api/v1, keyed by its Go identifier.
func declaredConditionTypes(t *testing.T) map[string]string {
	t.Helper()

	path := filepath.Join(repoRoot(t), conditionTypesFile)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoErrorf(t, err, "cannot parse %s; update conditionTypesFile if the declarations moved", path)

	declared := map[string]string{}
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 {
				continue
			}
			// The declared type is what identifies a condition constant. Matching on the
			// name prefix instead would pick up anything a future author calls
			// ConditionTypeSomething without giving it the type.
			typeIdent, ok := valueSpec.Type.(*ast.Ident)
			if !ok || typeIdent.Name != "ConditionType" {
				continue
			}
			literal, ok := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			require.NoError(t, err)
			declared[valueSpec.Names[0].Name] = value
		}
	}

	require.NotEmpty(t, declared,
		"parsed %s but found no ConditionType constants; the parser no longer matches the declarations", path)
	return declared
}

// TestConditionRegistryCoversEveryConditionType is the membership half: a condition type
// that reaches a CR without a declared owner and lifecycle fails here.
//
// [REGRESSION] The asymmetry that motivated it: `Ready` - the one condition every CR
// carries - was an unexported constant in internal/controller and not a ConditionType in
// api/v1 at all, so every table built from api/v1 silently omitted it.
func TestConditionRegistryCoversEveryConditionType(t *testing.T) {
	declared := declaredConditionTypes(t)

	registered := map[vkov1.ConditionType]int{}
	for _, row := range conditionRegistry {
		registered[row.conditionType]++
	}

	for identifier, value := range declared {
		count := registered[value]
		assert.Equalf(t, 1, count,
			"condition type %s (%q) appears %d times in conditionRegistry, want exactly 1: "+
				"a new condition needs a row in condition_registry.go declaring its owner, its kind "+
				"(level/edge/history) and its clear site",
			identifier, value, count)
	}

	values := map[string]struct{}{}
	for _, value := range declared {
		values[value] = struct{}{}
	}
	for conditionType := range registered {
		_, ok := values[conditionType]
		assert.Truef(t, ok,
			"conditionRegistry has a row for %q, which is not declared as a ConditionType in %s: "+
				"either the constant was removed and the row is stale, or the value has a typo",
			conditionType, conditionTypesFile)
	}
}

// TestConditionRegistryEdgesHaveAPresenceGuardedClear is the invariant the four historical
// fixes each established once: a condition nothing re-measures needs a site that provably
// knows the precondition is gone, and that site must not stamp the condition onto CRs that
// never carried it.
func TestConditionRegistryEdgesHaveAPresenceGuardedClear(t *testing.T) {
	for _, row := range conditionRegistry {
		if row.kind != conditionEdge || row.declaredGap != "" {
			continue
		}
		assert.NotEmptyf(t, row.clearSite,
			"%q is an edge with no clear site: nothing re-measures it, so a True lasts for the life "+
				"of the cluster. Add the clear at a site that proves the precondition is gone, or "+
				"declare the gap with a ticket reference",
			row.conditionType)
		assert.Truef(t, row.presenceGuarded,
			"%q is an edge whose clear is not presence-guarded: meta.SetStatusCondition ADDS an "+
				"absent condition and reports a change, so the first upgraded pass would write it onto "+
				"every CR in the fleet (ADR 0005 D10)",
			row.conditionType)
	}
}

// TestConditionRegistryLevelsHaveOneEvaluator guards the level-specific hazard. Two
// independent evaluators of one level mean the last writer of a pass wins, and which one
// is last is a function of step order rather than of anything about the cluster.
//
// More than one is legal only with an ownership rule that says which site decides. That
// escape was described in the evaluators docstring from the day the registry was written
// and implemented in 2026-08-26 with the T16 fix, which is the first row that needed it:
// StorageSpecNotApplied is raised by either StatefulSet reconciler and retracted only by
// the data one (ADR 0023 D4a, ADR 0027 D1).
func TestConditionRegistryLevelsHaveOneEvaluator(t *testing.T) {
	for _, row := range conditionRegistry {
		if row.kind != conditionLevel || row.declaredGap != "" {
			continue
		}
		if row.ownershipRule != "" {
			continue
		}
		assert.Equalf(t, 1, row.evaluators,
			"%q is a level with %d evaluators and no ownership rule: the last one to run in a pass "+
				"decides the stored value. Give it a single evaluator, or accumulate the verdicts and "+
				"write once, or name the rule in ownershipRule, or declare the gap with a ticket reference",
			row.conditionType, row.evaluators)
	}
}

// TestConditionRegistryOwnershipRulesAreEarned keeps the escape from becoming a way to
// silence the guard above, the same job TestConditionRegistryGapsAreTraceable does for
// declaredGap. A rule on a row that has only one evaluator claims to resolve a race that
// does not exist, and the next author reads it as licence to add a second site.
func TestConditionRegistryOwnershipRulesAreEarned(t *testing.T) {
	for _, row := range conditionRegistry {
		if row.ownershipRule == "" {
			continue
		}
		assert.Greaterf(t, row.evaluators, 1,
			"%q names an ownership rule but declares %d evaluator(s): a rule that arbitrates "+
				"between sites needs more than one site, and stating one here reads as permission "+
				"to add another without re-checking the invariant",
			row.conditionType, row.evaluators)
	}
}

// TestConditionRegistryHistoryIsNeverCleared pins the other direction. A verdict about a
// completed operation is meant to outlive the state it describes; a clear added to one is
// a discard, not a cleanup - which is exactly what ADR 0010 D15 refuses for
// TopologyRestored.
func TestConditionRegistryHistoryIsNeverCleared(t *testing.T) {
	for _, row := range conditionRegistry {
		if row.kind != conditionHistory {
			continue
		}
		assert.Emptyf(t, row.clearSite,
			"%q is history and has a clear site %q: rewriting a completed verdict destroys the only "+
				"durable record of what happened. If it should become a level, change its kind and say "+
				"so in its ADR",
			row.conditionType, row.clearSite)
	}
}

// TestConditionRegistryGapsAreTraceable keeps the escape hatch honest: a declared gap
// suppresses the invariants above, so it has to point at the item that owns the decision.
// Without this, "declaredGap: yes" becomes a way to silence the guard.
func TestConditionRegistryGapsAreTraceable(t *testing.T) {
	for _, row := range conditionRegistry {
		if row.declaredGap == "" {
			continue
		}
		assert.Regexpf(t, `T\d+`, row.declaredGap,
			"%q declares a gap that names no ticket item: a suppressed invariant has to be traceable "+
				"to a decision, or it is an undocumented defect with a comment on it",
			row.conditionType)
	}
}

// TestConditionRegistryNamesTheLoadBearingFields is documentation with teeth. It does not
// check behaviour; it fails when a row that something else reads as DATA loses that note,
// because the note is what tells the next author that rewriting the condition is
// destructive. MultipleMasters is the load-bearing case: splitBrainWarnAfter is measured
// from its stored LastTransitionTime.
func TestConditionRegistryNamesTheLoadBearingFields(t *testing.T) {
	loadBearing := map[vkov1.ConditionType]string{
		vkov1.ConditionTypeMultipleMasters:  "LastTransitionTime",
		vkov1.ConditionTypeTopologyRestored: "Message",
	}

	for _, row := range conditionRegistry {
		want, ok := loadBearing[row.conditionType]
		if !ok {
			continue
		}
		assert.Equalf(t, want, row.loadBearingField,
			"%q reads its %s as data, not as a report; the registry must keep saying so",
			row.conditionType, want)
	}
}
