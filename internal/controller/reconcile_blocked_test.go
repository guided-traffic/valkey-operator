package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// webhookUnreachableError reproduces the API server's answer when a
// failurePolicy=Fail webhook has no reachable backend — the exact shape logged
// during the 2026-08-19 infra-d incident.
func webhookUnreachableError() error {
	return apierrors.NewInternalError(fmt.Errorf(
		`failed calling webhook "mutate.kyverno.svc-fail": failed to call webhook: ` +
			`Post "https://kyverno-svc.kyverno.svc:443/mutate?timeout=10s": ` +
			`no endpoints available for service "kyverno-svc"`))
}

// webhookDeniedError reproduces an explicit denial by a policy webhook.
func webhookDeniedError() error {
	return apierrors.NewForbidden(
		corev1.Resource("configmaps"), "test",
		fmt.Errorf(`admission webhook "validate.kyverno.svc-fail" denied the request: `+
			`policy require-labels: validation error`))
}

func TestIsAdmissionRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"webhook unreachable", webhookUnreachableError(), true},
		{"webhook denied", webhookDeniedError(), true},
		{
			"wrapped webhook unreachable",
			fmt.Errorf("sentinel statefulset: %w", webhookUnreachableError()),
			true,
		},
		{"quota exceeded", apierrors.NewForbidden(
			corev1.Resource("pods"), "test", fmt.Errorf("exceeded quota")), false},
		{"conflict", apierrors.NewConflict(
			corev1.Resource("configmaps"), "test", fmt.Errorf("object was modified")), false},
		{"plain error", fmt.Errorf("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isAdmissionRejection(tt.err))
		})
	}
}

func TestReconcileBlockedReason(t *testing.T) {
	assert.Equal(t, vkov1.ReasonAdmissionWebhookDenied,
		reconcileBlockedReason(webhookUnreachableError()))
	assert.Equal(t, vkov1.ReasonAdmissionWebhookDenied,
		reconcileBlockedReason(webhookDeniedError()))
	assert.Equal(t, vkov1.ReasonWriteFailed,
		reconcileBlockedReason(fmt.Errorf("connection refused")))
}

func TestTruncateConditionMessage(t *testing.T) {
	short := "sentinel statefulset: blocked"
	assert.Equal(t, short, truncateConditionMessage(short))

	long := strings.Repeat("x", conditionMessageLimit+50)
	got := truncateConditionMessage(long)
	assert.Len(t, got, conditionMessageLimit+3)
	assert.True(t, strings.HasSuffix(got, "..."))
}

// blockedCondition reads the ReconcileBlocked condition from the stored CR.
func blockedCondition(t *testing.T, c client.Client, v *vkov1.Valkey) *metav1.Condition {
	t.Helper()
	stored := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, stored))
	return meta.FindStatusCondition(stored.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)
}

func resourceVersionOf(t *testing.T, c client.Client, v *vkov1.Valkey) string {
	t.Helper()
	stored := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, stored))
	return stored.ResourceVersion
}

func TestSetReconcileBlockedCondition_AdmissionRejection(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	err := fmt.Errorf("sentinel statefulset: %w", webhookUnreachableError())
	r.setReconcileBlockedCondition(context.Background(), v, err)

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond, "a failed reconcile must set ReconcileBlocked")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonAdmissionWebhookDenied, cond.Reason)
	assert.Contains(t, cond.Message, "mutate.kyverno.svc-fail",
		"the message must name the rejecting webhook")
}

func TestSetReconcileBlockedCondition_NonAdmissionError(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	r.setReconcileBlockedCondition(context.Background(), v,
		fmt.Errorf("Failed to reconcile ConfigMap: connection refused"))

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonWriteFailed, cond.Reason)
}

func TestSetReconcileBlockedCondition_ClearedAfterSuccess(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)
	ctx := context.Background()

	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	require.NotNil(t, blockedCondition(t, c, v))

	r.setReconcileBlockedCondition(ctx, v, nil)

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond, "the cleared condition stays on the CR as history")
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonReconcileSucceeded, cond.Reason)
}

func TestSetReconcileBlockedCondition_NoWriteWhenNeverBlocked(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	before := resourceVersionOf(t, c, v)
	r.setReconcileBlockedCondition(context.Background(), v, nil)

	assert.Nil(t, blockedCondition(t, c, v),
		"a healthy cluster that was never blocked must not gain a condition")
	assert.Equal(t, before, resourceVersionOf(t, c, v),
		"a healthy pass must not write status")
}

func TestSetReconcileBlockedCondition_NoWriteWhenUnchanged(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)
	ctx := context.Background()

	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	after := resourceVersionOf(t, c, v)

	// Same error again: a cluster blocked for minutes reconciles every few
	// seconds and must not rewrite an identical condition each time.
	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	assert.Equal(t, after, resourceVersionOf(t, c, v))

	// Repeating the cleared state must not write either.
	r.setReconcileBlockedCondition(ctx, v, nil)
	cleared := resourceVersionOf(t, c, v)
	r.setReconcileBlockedCondition(ctx, v, nil)
	assert.Equal(t, cleared, resourceVersionOf(t, c, v))
}

// newInterceptedReconciler builds a reconciler whose client fails selected
// writes, so a full Reconcile pass can be driven into its error path.
func newInterceptedReconciler(funcs interceptor.Funcs, objs ...client.Object) (*ValkeyReconciler, client.Client) {
	s := testScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&vkov1.Valkey{}, &appsv1.StatefulSet{}).
		WithInterceptorFuncs(funcs).
		Build()

	return &ValkeyReconciler{
		Client:          fakeClient,
		Scheme:          s,
		InstanceChecker: &mockInstanceChecker{},
		OperatorImage:   "ghcr.io/guided-traffic/valkey-operator:test",
		NewValkeyClientFn: func(addr, password string, tlsConfig *tls.Config) *valkeyclient.Client {
			_, port, _ := net.SplitHostPort(addr)
			if port == "" {
				port = "1"
			}
			return valkeyclient.New("127.0.0.1:" + port)
		},
	}, fakeClient
}

// TestReconcile_SetsReconcileBlockedOnAdmissionRejection is the unit-level form
// of T4: a webhook rejecting a managed write must be readable off the CR.
func TestReconcile_SetsReconcileBlockedOnAdmissionRejection(t *testing.T) {
	v := newTestValkey("test", "default")
	rejectConfigMaps := interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.ConfigMap); ok {
				return webhookUnreachableError()
			}
			return c.Create(ctx, obj, opts...)
		},
	}
	r, c := newInterceptedReconciler(rejectConfigMaps, v)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: v.Name, Namespace: v.Namespace},
	})
	require.Error(t, err, "the rejected write must still surface as a reconcile error")

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond, "the CR must carry ReconcileBlocked while writes are rejected")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonAdmissionWebhookDenied, cond.Reason)
	assert.Contains(t, cond.Message, "mutate.kyverno.svc-fail")
}

// TestReconcile_ClearsReconcileBlockedAfterSuccess covers the True->False
// transition across a Reconcile pass, the second half of T4.
func TestReconcile_ClearsReconcileBlockedAfterSuccess(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)
	ctx := context.Background()

	// Seed the blocked state as a previous failing pass would have left it.
	r.setReconcileBlockedCondition(ctx, v, webhookUnreachableError())
	require.Equal(t, metav1.ConditionTrue, blockedCondition(t, c, v).Status)

	_, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: v.Name, Namespace: v.Namespace},
	})
	require.NoError(t, err)

	cond := blockedCondition(t, c, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status,
		"a fully successful pass must clear ReconcileBlocked")
	assert.Equal(t, vkov1.ReasonReconcileSucceeded, cond.Reason)
}
