package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// Tests in this file cover ADR 0001: a rejected write on one sub-resource must not
// silence the rest of the reconcile pass.

// --- runReconcileSteps ---

func TestRunReconcileSteps_RunsEveryStepDespiteFailure(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	var ran []string
	record := func(name string, err error) reconcileStep {
		return reconcileStep{name: name, run: func(context.Context, *vkov1.Valkey) error {
			ran = append(ran, name)
			return err
		}}
	}

	err := r.runReconcileSteps(context.Background(), v, []reconcileStep{
		record("first", nil),
		record("second", fmt.Errorf("rejected")),
		record("third", nil),
		record("fourth", fmt.Errorf("also rejected")),
	})

	assert.Equal(t, []string{"first", "second", "third", "fourth"}, ran,
		"a failing step must not stop the ones behind it")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "second: rejected")
	assert.Contains(t, err.Error(), "fourth: also rejected",
		"every failure must survive into the joined error")
}

func TestRunReconcileSteps_SkipsInapplicableSteps(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	ran := false
	err := r.runReconcileSteps(context.Background(), v, []reconcileStep{{
		name: "sentinel",
		when: (*vkov1.Valkey).IsSentinelEnabled,
		run: func(context.Context, *vkov1.Valkey) error {
			ran = true
			return fmt.Errorf("must not run")
		},
	}})

	assert.False(t, ran, "a step whose predicate is false must not run")
	assert.NoError(t, err)
}

func TestRunReconcileSteps_NoErrorWhenAllSucceed(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	err := r.runReconcileSteps(context.Background(), v, []reconcileStep{
		{name: "ok", run: func(context.Context, *vkov1.Valkey) error { return nil }},
	})
	assert.NoError(t, err)
}

func TestCompactErrorMessage(t *testing.T) {
	assert.Empty(t, compactErrorMessage(nil))

	joined := fmt.Errorf("first: rejected\nsecond: rejected")
	got := compactErrorMessage(joined)
	assert.NotContains(t, got, "\n", "a status message must stay on one line")
	assert.Equal(t, "first: rejected; second: rejected", got)
}

// --- reconcileResources ---

// newBlockedValkey returns a CR that exercises the steps behind the StatefulSet:
// NetworkPolicies and the metrics Service.
func newBlockedValkey() *vkov1.Valkey {
	return newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	})
}

// rejectCreateOf fails CREATE for objects of the same type as blocked.
func rejectCreateOf(blocked client.Object) interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.CreateOption) error {
			if fmt.Sprintf("%T", obj) == fmt.Sprintf("%T", blocked) {
				return webhookUnreachableError()
			}
			return c.Create(ctx, obj, opts...)
		},
	}
}

func TestReconcileResources_ContinuesPastRejectedStatefulSet(t *testing.T) {
	v := newBlockedValkey()
	r, c := newInterceptedReconciler(rejectCreateOf(&appsv1.StatefulSet{}), v)
	ctx := context.Background()

	err := r.reconcileResources(ctx, v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "StatefulSet:", "the joined error must name the failing step")

	// Everything behind the failing step must still exist. This is the F1 gap:
	// on 1.10.46 a single rejection ended the pass here.
	np := &networkingv1.NetworkPolicy{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: builder.BuildValkeyNetworkPolicy(v, "").Name, Namespace: v.Namespace}, np),
		"NetworkPolicies must be reconciled even though the StatefulSet write was rejected")

	svc := &corev1.Service{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: builder.MetricsServiceName(v), Namespace: v.Namespace}, svc),
		"the metrics Service must be reconciled even though the StatefulSet write was rejected")
}

func TestReconcileResources_ContinuesPastRejectedConfigMap(t *testing.T) {
	v := newBlockedValkey()
	r, c := newInterceptedReconciler(rejectCreateOf(&corev1.ConfigMap{}), v)
	ctx := context.Background()

	err := r.reconcileResources(ctx, v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ConfigMap:")

	// The StatefulSet references the ConfigMap by name only, so it is still
	// written — its pods stay pending until the ConfigMap lands.
	sts := &appsv1.StatefulSet{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: v.Namespace}, sts),
		"a rejected ConfigMap must not skip the StatefulSet")
}

func TestReconcileResources_JoinsFailuresOfEveryStep(t *testing.T) {
	v := newBlockedValkey()
	rejectBoth := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.CreateOption) error {
			switch obj.(type) {
			case *corev1.ConfigMap, *appsv1.StatefulSet:
				return webhookUnreachableError()
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	r, _ := newInterceptedReconciler(rejectBoth, v)

	err := r.reconcileResources(context.Background(), v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ConfigMap:")
	assert.Contains(t, err.Error(), "StatefulSet:")
	assert.Equal(t, 2, strings.Count(err.Error(), "mutate.kyverno.svc-fail"),
		"both rejections must be reported, not just the first")
}

func TestReconcileResources_NoErrorOnHealthyPass(t *testing.T) {
	v := newBlockedValkey()
	r, _ := newTestReconciler(v)
	assert.NoError(t, r.reconcileResources(context.Background(), v))
}

// --- full Reconcile pass ---

// TestReconcile_ReconcilesDataPlaneWhileBlocked is the unit-level form of
// ADR 0001 D1, D4: while one managed write is rejected, the rest of the pass —
// including the StatefulSet and the status update — still runs.
func TestReconcile_ReconcilesDataPlaneWhileBlocked(t *testing.T) {
	v := newBlockedValkey()
	r, c := newInterceptedReconciler(rejectCreateOf(&networkingv1.NetworkPolicy{}), v)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: v.Name, Namespace: v.Namespace},
	})
	require.Error(t, err, "the rejected write must still surface as a reconcile error")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: v.Namespace}, sts),
		"the data StatefulSet must be reconciled while a NetworkPolicy write is rejected")

	stored := &vkov1.Valkey{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, stored))
	assert.Equal(t, vkov1.ValkeyPhaseError, stored.Status.Phase)
	assert.Contains(t, stored.Status.Message, "NetworkPolicies:",
		"the phase message must name the failing step")
	assert.NotContains(t, stored.Status.Message, "\n",
		"the phase message must stay readable in kubectl/Lens")
}

// --- step order ---

// The sidecar Role grants patch on named pods (ADR 0012 D8 step 3). On a scale-up
// the Role must name pod N before the StatefulSet write creates it, or that pod's
// sidecar 403s on its own role label until the next pass. The order of these two
// steps is the whole guarantee, so it is asserted rather than assumed.
func TestResourceReconcileSteps_RBACBeforeStatefulSet(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	indexOf := func(name string) int {
		for i, step := range r.resourceReconcileSteps() {
			if step.name == name {
				return i
			}
		}
		return -1
	}

	rbac, sts := indexOf("sidecar RBAC"), indexOf("StatefulSet")
	require.NotEqual(t, -1, rbac, "the sidecar RBAC step must exist")
	require.NotEqual(t, -1, sts, "the StatefulSet step must exist")
	assert.Less(t, rbac, sts,
		"the sidecar Role has to name a scale-up pod before the StatefulSet creates it")
}

// StorageSpecNotApplied has two evaluators and one clear authority: either
// StatefulSet reconciler may report a claim conflict, only the data one may clear
// (ADR 0023 D4a). That rule is expressed as a mayClear argument at the two call
// sites, and it is correct only while the data step runs first — otherwise the
// Sentinel tier's report would be erased by the data tier's clear in the same pass,
// which is the same defect mirrored. Nothing else pins this order, and swapping the
// two steps leaves every other test in the package green.
func TestResourceReconcileSteps_StatefulSetBeforeSentinelResources(t *testing.T) {
	v := newTestValkey("test", "default", haCluster)
	r, _ := newTestReconciler(v)

	indexOf := func(name string) int {
		for i, step := range r.resourceReconcileSteps() {
			if step.name == name {
				return i
			}
		}
		return -1
	}

	data, sentinel := indexOf("StatefulSet"), indexOf("Sentinel resources")
	require.NotEqual(t, -1, data, "the data StatefulSet step must exist")
	require.NotEqual(t, -1, sentinel, "the Sentinel resources step must exist")
	assert.Less(t, data, sentinel,
		"the clear authority for StorageSpecNotApplied is the data tier, which only holds "+
			"while it runs first")
}
