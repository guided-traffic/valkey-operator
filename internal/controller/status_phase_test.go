package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// Tests in this file cover NA3 of the admission-gap ticket: while
// reconcileResources fails but the data plane is healthy, the pass used to write
// the phase twice with opposite values (health phase from updateStatus, then
// Error), so watchers saw the CR oscillate on every pass.

// phaseRecorder collects the phase of every status write of a Valkey CR.
type phaseRecorder struct {
	phases []vkov1.ValkeyPhase
}

// intercept adds the recorder to funcs, keeping whatever else funcs already does.
func (p *phaseRecorder) intercept(funcs interceptor.Funcs) interceptor.Funcs {
	funcs.SubResourceUpdate = func(ctx context.Context, c client.Client, subResource string,
		obj client.Object, opts ...client.SubResourceUpdateOption) error {
		if v, ok := obj.(*vkov1.Valkey); ok && subResource == "status" {
			p.phases = append(p.phases, v.Status.Phase)
		}
		return c.SubResource(subResource).Update(ctx, obj, opts...)
	}
	return funcs
}

func (p *phaseRecorder) reset() { p.phases = nil }

// transitions counts how often a recorded write changed the phase, starting from
// the phase the CR had before the pass.
func (p *phaseRecorder) transitions(from vkov1.ValkeyPhase) int {
	changes := 0
	prev := from
	for _, phase := range p.phases {
		if phase != prev {
			changes++
			prev = phase
		}
	}
	return changes
}

// markStatefulSetReady drives the data StatefulSet to fully ready so the health
// phase computes to OK.
func markStatefulSetReady(t *testing.T, c client.Client, v *vkov1.Valkey) {
	t.Helper()
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: v.Namespace}, sts))
	sts.Status.Replicas = v.Spec.Replicas
	sts.Status.ReadyReplicas = v.Spec.Replicas
	require.NoError(t, c.Status().Update(context.Background(), sts))
}

func reconcileFor(t *testing.T, r *ValkeyReconciler, v *vkov1.Valkey) error {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: v.Name, Namespace: v.Namespace},
	})
	return err
}

// TestReconcile_BlockedPassDoesNotFlapPhase is the unit-level form of NA3: a
// healthy data plane behind a rejected managed write must not produce an OK write
// followed by an Error write on every pass.
func TestReconcile_BlockedPassDoesNotFlapPhase(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	rec := &phaseRecorder{}
	r, c := newInterceptedReconciler(rec.intercept(rejectCreateOf(&networkingv1.NetworkPolicy{})), v)

	// First pass creates the StatefulSet; the NetworkPolicy write is rejected.
	require.Error(t, reconcileFor(t, r, v))
	markStatefulSetReady(t, c, v)

	before := crGet(t, c, v.Name)
	require.Equal(t, vkov1.ValkeyPhaseError, before.Status.Phase,
		"precondition: the blocked first pass leaves the CR in Error")

	// Second pass: data plane is fully ready (health phase would be OK), the
	// NetworkPolicy write is still rejected.
	rec.reset()
	require.Error(t, reconcileFor(t, r, v))

	assert.NotContains(t, rec.phases, vkov1.ValkeyPhaseOK,
		"a blocked pass must never write the health phase; the CR would flap OK<->Error")
	assert.Zero(t, rec.transitions(before.Status.Phase),
		"a blocked pass must not change the phase it already reported")

	after := crGet(t, c, v.Name)
	assert.Equal(t, vkov1.ValkeyPhaseError, after.Status.Phase)
	assert.Contains(t, after.Status.Message, "NetworkPolicies:",
		"the phase message must keep naming the failing step")
	assert.Equal(t, v.Spec.Replicas, after.Status.ReadyReplicas,
		"suppressing the phase must not suppress the rest of the status")
}

// TestReconcile_BlockedPassRecoversToHealthPhase guards the other direction: once
// the rejected write succeeds, the health phase must take over again.
func TestReconcile_BlockedPassRecoversToHealthPhase(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	blocked := true
	rec := &phaseRecorder{}
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.CreateOption) error {
			if _, ok := obj.(*networkingv1.NetworkPolicy); ok && blocked {
				return webhookUnreachableError()
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	r, c := newInterceptedReconciler(rec.intercept(funcs), v)

	require.Error(t, reconcileFor(t, r, v))
	markStatefulSetReady(t, c, v)

	blocked = false
	rec.reset()
	require.NoError(t, reconcileFor(t, r, v))

	assert.Contains(t, rec.phases, vkov1.ValkeyPhaseOK,
		"an unblocked pass must report the health phase again")
	assert.Equal(t, vkov1.ValkeyPhaseOK, crGet(t, c, v.Name).Status.Phase)
}

// TestReconcile_BlockedPassWritesPhaseEvenWhenWorkloadFails settles the open datum
// recorded with NA3: a CR was sampled as Provisioning while blocked, which the
// final Error write should have overwritten. An early return from the workload
// pass used to skip that write entirely.
func TestReconcile_BlockedPassWritesPhaseEvenWhenWorkloadFails(t *testing.T) {
	v := newTestValkey("test", "default")
	// A StatefulSet read that fails with something other than NotFound blocks the
	// managed writes and fails the workload pass behind them.
	r, c := newInterceptedReconciler(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				return apierrors.NewServiceUnavailable("apiserver is down")
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}, v)

	require.Error(t, reconcileFor(t, r, v))

	stored := crGet(t, c, v.Name)
	assert.Equal(t, vkov1.ValkeyPhaseError, stored.Status.Phase,
		"the blocked pass owns the phase even when the workload pass returned early")
	assert.Contains(t, stored.Status.Message, "Failed to reconcile resources:")
}

// --- phase write suppression ---

func TestUpdatePhase_SuppressedWhileBlocked(t *testing.T) {
	v := newTestValkey("test", "default")
	v.Status.Phase = vkov1.ValkeyPhaseError
	v.Status.Message = "Failed to reconcile resources: NetworkPolicies: rejected"
	r, c := newTestReconciler(v)
	ctx := withBlockedPass(context.Background())

	require.NoError(t, r.updatePhase(ctx, v, vkov1.ValkeyPhaseOK, "All replicas are ready"))
	assert.Equal(t, vkov1.ValkeyPhaseError, crGet(t, c, v.Name).Status.Phase,
		"intermediate phase writes must be dropped while the pass is blocked")

	require.NoError(t, r.writePhase(ctx, v, vkov1.ValkeyPhaseError, "Failed to reconcile resources: later"))
	stored := crGet(t, c, v.Name)
	assert.Equal(t, "Failed to reconcile resources: later", stored.Status.Message,
		"writePhase is the one write a blocked pass is allowed to make")
}

func TestUpdatePhase_WritesWhenPassIsNotBlocked(t *testing.T) {
	v := newTestValkey("test", "default")
	v.Status.Phase = vkov1.ValkeyPhaseProvisioning
	r, c := newTestReconciler(v)

	require.NoError(t, r.updatePhase(context.Background(), v, vkov1.ValkeyPhaseOK, "All replicas are ready"))
	assert.Equal(t, vkov1.ValkeyPhaseOK, crGet(t, c, v.Name).Status.Phase)
}

// TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked pins the middle ground NA3
// asks for: only the phase and its message are suppressed, everything the data
// plane reports keeps flowing into the status.
func TestUpdateStatus_KeepsNonPhaseFieldsWhileBlocked(t *testing.T) {
	v := newTestValkey("test", "default")
	v.Status.Phase = vkov1.ValkeyPhaseError
	v.Status.Message = "Failed to reconcile resources: NetworkPolicies: rejected"
	r, c := newTestReconciler(v)
	require.NoError(t, reconcileFor(t, r, v))
	markStatefulSetReady(t, c, v)

	stored := crGet(t, c, v.Name)
	stored.Status.Phase = vkov1.ValkeyPhaseError
	stored.Status.Message = "Failed to reconcile resources: NetworkPolicies: rejected"
	require.NoError(t, c.Status().Update(context.Background(), stored))

	require.NoError(t, r.updateStatus(withBlockedPass(context.Background()), stored))

	after := crGet(t, c, v.Name)
	assert.Equal(t, vkov1.ValkeyPhaseError, after.Status.Phase)
	assert.Equal(t, "Failed to reconcile resources: NetworkPolicies: rejected", after.Status.Message)
	assert.Equal(t, v.Spec.Replicas, after.Status.ReadyReplicas,
		"readyReplicas must keep tracking the data plane while blocked")
	assert.Equal(t, "test-0", after.Status.MasterPod,
		"masterPod must keep tracking the data plane while blocked")
}

// --- NA33: a rejected initial phase write must not own the pass ---

// rejectFirstValkeyStatusWrite fails the first status subresource write of a
// Valkey CR and lets every later one through, keeping whatever else funcs
// already does. It reproduces the NA33 trigger: a webhook guarding the CR status
// subresource, or a momentarily lost valkeys/status RBAC, catching the initial
// Provisioning write of a brand-new CR.
func rejectFirstValkeyStatusWrite(funcs interceptor.Funcs, rejected *int) interceptor.Funcs {
	funcs.SubResourceUpdate = func(ctx context.Context, c client.Client, subResource string,
		obj client.Object, opts ...client.SubResourceUpdateOption) error {
		if _, ok := obj.(*vkov1.Valkey); ok && subResource == "status" && *rejected == 0 {
			*rejected++
			return webhookUnreachableError()
		}
		return c.SubResource(subResource).Update(ctx, obj, opts...)
	}
	return funcs
}

// TestReconcile_InitialPhaseWriteFailureDoesNotAbortPass pins NA33: the initial
// Provisioning write used to return the pass, so a rejected status write meant
// reconcileResources never ran and the CR got neither its managed resources nor
// a phase.
func TestReconcile_InitialPhaseWriteFailureDoesNotAbortPass(t *testing.T) {
	v := newTestValkey("test", "default")
	rejected := 0
	r, c := newInterceptedReconciler(rejectFirstValkeyStatusWrite(interceptor.Funcs{}, &rejected), v)

	require.NoError(t, reconcileFor(t, r, v),
		"a rejected initial phase write must not fail the pass; the phase is written again later in it")
	require.Equal(t, 1, rejected, "precondition: the initial phase write is the rejected one")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name:      common.StatefulSetName(v, common.ComponentValkey),
		Namespace: v.Namespace,
	}, sts), "reconcileResources must run even when the first status write was rejected")

	assert.Equal(t, vkov1.ValkeyPhaseProvisioning, crGet(t, c, v.Name).Status.Phase,
		"the later status write of the same pass must fill the phase in")
}

// TestReconcile_InitialPhaseWriteFailureStillReportsReconcileBlocked is the half
// of NA33 that matters for observability: with the early return, a CR whose first
// status write was rejected carried no ReconcileBlocked condition either, so the
// exact failure class that condition exists to surface stayed invisible.
func TestReconcile_InitialPhaseWriteFailureStillReportsReconcileBlocked(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	rejected := 0
	r, c := newInterceptedReconciler(
		rejectFirstValkeyStatusWrite(rejectCreateOf(&networkingv1.NetworkPolicy{}), &rejected), v)

	require.Error(t, reconcileFor(t, r, v), "the rejected managed write must still surface as an error")
	require.Equal(t, 1, rejected, "precondition: the initial phase write is the rejected one")

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond,
		"a CR whose initial phase write was rejected must still report why its resources are blocked")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonAdmissionWebhookDenied, cond.Reason)

	assert.Equal(t, vkov1.ValkeyPhaseError, crGet(t, c, v.Name).Status.Phase,
		"the single phase write of the blocked pass must still land")
}
