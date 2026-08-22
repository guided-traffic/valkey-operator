//go:build integration

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestPodDisruptionBudgetUIDPrecondition_Integration proves that a real API
// server enforces the UID delete precondition the PDB cleanup sends
// (internal/controller/pdb.go: r.Delete(ctx, pdb, client.Preconditions{UID: &pdb.UID}),
// ADR 0006 D8, D9).
//
// Why this lives here and not in a unit test (docs/adr/0017-test-and-ci-policy.md, D12): the
// controller-runtime fake client implements only the ResourceVersion precondition
// (sigs.k8s.io/controller-runtime@v0.24.1/pkg/client/fake/client.go, deleteObject) and ignores the
// UID one, so a unit test can assert that the option is sent and can inject a Conflict, but it can
// never show the rejection actually happening. Against envtest it happens for real.
//
// What this test does NOT prove, stated so nobody reads more into it: it does not
// schedule the interleaving itself. The race the precondition guards is a
// cache-backed Get that observed the operator budget, followed by a Delete after
// the object under that name was replaced. Reproducing that against a running
// controller means winning a race with informer delivery, which is not a test, it
// is a coin flip. The two halves are covered separately and deliberately: the unit
// tests pin that the operator sends the inspected object's UID, and this test pins
// that an API server honours it.
func TestPodDisruptionBudgetUIDPrecondition_Integration(t *testing.T) {
	ctx := testCtx
	name := types.NamespacedName{Name: "uid-precondition-test", Namespace: "default"}

	// A direct client, not the manager's: this test is about what the API server
	// does with a delete option, so every read has to come from the API server
	// rather than from an informer cache that may lag a recreate by a few
	// milliseconds.
	direct, err := client.New(testEnv.Config, client.Options{Scheme: scheme.Scheme})
	require.NoError(t, err, "building a direct (uncached) client")

	build := func() *policyv1.PodDisruptionBudget {
		maxUnavailable := intstr.FromInt32(1)
		return &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Name: name.Name, Namespace: name.Namespace},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MaxUnavailable: &maxUnavailable,
				Selector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app": "uid-precondition"}},
			},
		}
	}

	// The budget the operator would have inspected.
	inspected := build()
	require.NoError(t, direct.Create(ctx, inspected))
	inspectedUID := inspected.UID
	require.NotEmpty(t, inspectedUID)

	// It disappears and a different object takes the name over - the user's own
	// budget in the ADR 0006 D8, D9 scenario. Same name, new identity.
	require.NoError(t, direct.Delete(ctx, inspected))
	foreign := build()
	require.NoError(t, direct.Create(ctx, foreign))
	foreignUID := foreign.UID
	require.NotEqual(t, inspectedUID, foreignUID, "the recreated budget must be a different object")
	defer func() { _ = direct.Delete(ctx, build()) }()

	t.Run("a delete carrying the stale UID is rejected and the foreign budget survives", func(t *testing.T) {
		stale := build()
		stale.UID = inspectedUID

		err := direct.Delete(ctx, stale, client.Preconditions{UID: &inspectedUID})
		require.Error(t, err, "the API server must refuse a delete whose UID precondition does not match")
		assert.True(t, apierrors.IsConflict(err), "expected a 409 Conflict, got %v", err)

		survivor := &policyv1.PodDisruptionBudget{}
		require.NoError(t, direct.Get(ctx, name, survivor), "the foreign budget must still exist")
		assert.Equal(t, foreignUID, survivor.UID, "the surviving object must be the foreign one")
	})

	t.Run("a delete carrying the matching UID succeeds", func(t *testing.T) {
		current := build()
		require.NoError(t, direct.Delete(ctx, current, client.Preconditions{UID: &foreignUID}),
			"the precondition must not block the delete of the object it names")

		gone := &policyv1.PodDisruptionBudget{}
		err := direct.Get(ctx, name, gone)
		assert.True(t, apierrors.IsNotFound(err), "expected the budget to be gone, got %v", err)
	})
}
