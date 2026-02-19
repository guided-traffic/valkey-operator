package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/health"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = vkov1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	return s
}

// mockInstanceChecker implements InstanceChecker for unit tests.
type mockInstanceChecker struct {
	pingErr      error
	clusterState *health.ClusterState
}

func (m *mockInstanceChecker) PingPod(_ context.Context, _ *vkov1.Valkey, _ string) error {
	return m.pingErr
}

func (m *mockInstanceChecker) CheckCluster(_ context.Context, v *vkov1.Valkey) *health.ClusterState {
	if m.clusterState != nil {
		return m.clusterState
	}
	// Default: healthy cluster.
	return &health.ClusterState{
		MasterPod:          fmt.Sprintf("%s-0", v.Name),
		ReadyReplicas:      v.Spec.Replicas - 1,
		TotalReplicas:      v.Spec.Replicas - 1,
		AllSynced:          true,
		SentinelMonitoring: v.IsSentinelEnabled(),
	}
}

func newTestReconciler(objs ...client.Object) (*ValkeyReconciler, client.Client) {
	s := testScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&vkov1.Valkey{}, &appsv1.StatefulSet{}).
		Build()

	return &ValkeyReconciler{
		Client:          fakeClient,
		Scheme:          s,
		InstanceChecker: &mockInstanceChecker{},
	}, fakeClient
}

func newTestValkey(name, ns string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: vkov1.ValkeySpec{
			Replicas: 1,
			Image:    "valkey/valkey:8.0",
		},
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

func reconcileOnce(t *testing.T, r *ValkeyReconciler, name, ns string) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
	require.NoError(t, err)
	return result
}

// --- Basic Reconcile ---

func TestReconcile_ResourceNotFound(t *testing.T) {
	r, _ := newTestReconciler()

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "default"},
	})

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcile_CreatesConfigMap(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: builder.ConfigMapName(v), Namespace: "default",
	}, cm)

	require.NoError(t, err)
	assert.Equal(t, "test-config", cm.Name)
	assert.Contains(t, cm.Data, builder.ValkeyConfigKey)
	assert.Contains(t, cm.Data[builder.ValkeyConfigKey], "bind 0.0.0.0")
}

func TestReconcile_CreatesHeadlessService(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-headless", Namespace: "default",
	}, svc)

	require.NoError(t, err)
	assert.Equal(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.True(t, svc.Spec.PublishNotReadyAddresses)
}

func TestReconcile_CreatesClientService(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, svc)

	require.NoError(t, err)
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(6379), svc.Spec.Ports[0].Port)
}

func TestReconcile_CreatesStatefulSet(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)

	require.NoError(t, err)
	assert.Equal(t, int32(1), *sts.Spec.Replicas)
	assert.Equal(t, "valkey/valkey:8.0", sts.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, "test-headless", sts.Spec.ServiceName)
}

// --- Idempotent Reconcile ---

func TestReconcile_Idempotent(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	// Reconcile multiple times — should not error.
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
}

// TestReconcile_Idempotent_NoUnnecessaryStatusUpdates verifies that repeated
// reconciles with no spec or readiness changes do not write the status,
// preventing infinite reconcile loops caused by status update watch events.
func TestReconcile_Idempotent_NoUnnecessaryStatusUpdates(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// First reconcile creates resources and sets initial status.
	reconcileOnce(t, r, "test", "default")

	// Capture the resource version after the first reconcile.
	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)
	rvAfterFirst := v.ResourceVersion

	// Second reconcile — nothing changed, should NOT update status.
	reconcileOnce(t, r, "test", "default")

	err = c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)
	rvAfterSecond := v.ResourceVersion

	assert.Equal(t, rvAfterFirst, rvAfterSecond,
		"ResourceVersion should not change on idempotent reconcile — status should not be rewritten")
}

// TestReconcile_HA_Idempotent_NoUnnecessaryStatusUpdates verifies that
// HA mode reconciles do not trigger unnecessary status updates.
func TestReconcile_HA_Idempotent_NoUnnecessaryStatusUpdates(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	// First reconcile.
	reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)
	rvAfterFirst := v.ResourceVersion

	// Second reconcile — no changes, should NOT update status.
	reconcileOnce(t, r, "test", "default")

	err = c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)
	rvAfterSecond := v.ResourceVersion

	assert.Equal(t, rvAfterFirst, rvAfterSecond,
		"ResourceVersion should not change on idempotent HA reconcile — status should not be rewritten")
}

// TestStatusUnchanged_DetectsChanges verifies the statusUnchanged helper.
func TestStatusUnchanged_DetectsChanges(t *testing.T) {
	base := &vkov1.ValkeyStatus{
		Phase:         vkov1.ValkeyPhaseProvisioning,
		Message:       "test message",
		ReadyReplicas: 1,
		MasterPod:     "test-0",
	}

	// Same values — should be unchanged.
	same := base.DeepCopy()
	assert.True(t, statusUnchanged(base, same), "identical status should be unchanged")

	// Different phase — should be changed.
	diffPhase := base.DeepCopy()
	diffPhase.Phase = vkov1.ValkeyPhaseOK
	assert.False(t, statusUnchanged(base, diffPhase), "different phase should be detected")

	// Different message — should be changed.
	diffMessage := base.DeepCopy()
	diffMessage.Message = "new message"
	assert.False(t, statusUnchanged(base, diffMessage), "different message should be detected")

	// Different readyReplicas — should be changed.
	diffReplicas := base.DeepCopy()
	diffReplicas.ReadyReplicas = 3
	assert.False(t, statusUnchanged(base, diffReplicas), "different readyReplicas should be detected")

	// Different masterPod — should be changed.
	diffMaster := base.DeepCopy()
	diffMaster.MasterPod = "test-1"
	assert.False(t, statusUnchanged(base, diffMaster), "different masterPod should be detected")
}

// TestCleanseCertificateSpec_RemovesPrivateKey verifies that cert-manager
// webhook-added fields are removed before comparison.
func TestCleanseCertificateSpec_RemovesPrivateKey(t *testing.T) {
	// Simulate a spec as returned by Kubernetes (with webhook-added fields).
	specWithWebhookFields := map[string]interface{}{
		"secretName": "test-tls",
		"issuerRef": map[string]interface{}{
			"name": "my-issuer",
			"kind": "ClusterIssuer",
		},
		"privateKey": map[string]interface{}{
			"rotationPolicy": "Always",
		},
	}

	// Simulate the desired spec (without webhook fields).
	desiredSpec := map[string]interface{}{
		"secretName": "test-tls",
		"issuerRef": map[string]interface{}{
			"name": "my-issuer",
			"kind": "ClusterIssuer",
		},
	}

	// Without cleansing, they differ.
	assert.NotEqual(t, specWithWebhookFields, desiredSpec,
		"specs should differ before cleansing")

	// After cleansing, they should match.
	cleanseCertificateSpec(specWithWebhookFields)
	cleanseCertificateSpec(desiredSpec)
	assert.Equal(t, specWithWebhookFields, desiredSpec,
		"specs should match after cleansing webhook-added fields")
}

// --- ConfigMap Update ---

func TestReconcile_UpdatesConfigMapOnSpecChange(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Enable persistence.
	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	v.Spec.Persistence = &vkov1.PersistenceSpec{
		Enabled: true,
		Mode:    vkov1.PersistenceModeRDB,
		Size:    resource.MustParse("1Gi"),
	}
	err = c.Update(context.Background(), v)
	require.NoError(t, err)

	reconcileOnce(t, r, "test", "default")

	cm := &corev1.ConfigMap{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name: "test-config", Namespace: "default",
	}, cm)
	require.NoError(t, err)

	// Should now contain RDB save directives.
	assert.Contains(t, cm.Data[builder.ValkeyConfigKey], "save 900 1")
}

// --- StatefulSet Update ---

func TestReconcile_UpdatesStatefulSetOnImageChange(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Change image.
	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	v.Spec.Image = "valkey/valkey:9.0"
	err = c.Update(context.Background(), v)
	require.NoError(t, err)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)
	require.NoError(t, err)

	assert.Equal(t, "valkey/valkey:9.0", sts.Spec.Template.Spec.Containers[0].Image)
}

func TestReconcile_UpdatesStatefulSetOnReplicaChange(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Scale up.
	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	v.Spec.Replicas = 3
	err = c.Update(context.Background(), v)
	require.NoError(t, err)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)
	require.NoError(t, err)

	assert.Equal(t, int32(3), *sts.Spec.Replicas)
}

// --- Status ---

func TestReconcile_SetsProvisioningPhase(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	// With fake client, StatefulSet has 0 ready replicas — should be Provisioning.
	assert.Equal(t, vkov1.ValkeyPhaseProvisioning, v.Status.Phase)
	assert.Contains(t, v.Status.Message, "ready")
}

// --- Connectivity Check ---

func TestReconcile_Standalone_OK_WhenConnectivitySucceeds(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// First reconcile creates resources.
	reconcileOnce(t, r, "test", "default")

	// Simulate all replicas ready by updating StatefulSet status.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	sts.Status.ReadyReplicas = 1
	require.NoError(t, c.Status().Update(context.Background(), sts))

	// Second reconcile should report OK (mock ping succeeds by default).
	reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	assert.Equal(t, vkov1.ValkeyPhaseOK, v.Status.Phase)
	assert.Equal(t, "All replicas are ready", v.Status.Message)
	assert.Equal(t, "test-0", v.Status.MasterPod)
}

func TestReconcile_Standalone_Error_WhenConnectivityFails(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// Inject failing connectivity checker.
	r.InstanceChecker = &mockInstanceChecker{
		pingErr: fmt.Errorf("dial tcp: connection refused"),
	}

	// First reconcile creates resources.
	reconcileOnce(t, r, "test", "default")

	// Simulate all replicas ready.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	sts.Status.ReadyReplicas = 1
	require.NoError(t, c.Status().Update(context.Background(), sts))

	// Second reconcile should detect connectivity failure.
	result := reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	assert.Equal(t, vkov1.ValkeyPhaseError, v.Status.Phase)
	assert.Contains(t, v.Status.Message, "unreachable")
	assert.Contains(t, v.Status.Message, "connection refused")

	// Must requeue so transient errors are retried.
	assert.NotZero(t, result.RequeueAfter, "Error phase must trigger a requeue")
}

func TestReconcile_HA_OK_WhenClusterHealthy(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	// First reconcile creates resources.
	reconcileOnce(t, r, "test", "default")

	// Simulate all Valkey replicas ready.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	sts.Status.ReadyReplicas = 3
	require.NoError(t, c.Status().Update(context.Background(), sts))

	// Simulate all Sentinel replicas ready.
	sentinelSts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test-sentinel", Namespace: "default"}, sentinelSts))
	sentinelSts.Status.ReadyReplicas = 3
	require.NoError(t, c.Status().Update(context.Background(), sentinelSts))

	// Second reconcile should report OK (mock cluster check succeeds).
	reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	assert.Equal(t, vkov1.ValkeyPhaseOK, v.Status.Phase)
	assert.Contains(t, v.Status.Message, "HA cluster ready")
	assert.Equal(t, "test-0", v.Status.MasterPod)
}

func TestReconcile_HA_Error_WhenClusterUnreachable(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	// Inject failing cluster check (simulates network policy blocking).
	r.InstanceChecker = &mockInstanceChecker{
		clusterState: &health.ClusterState{
			Error: fmt.Errorf("no master found among 3 pods"),
		},
	}

	// First reconcile creates resources.
	reconcileOnce(t, r, "test", "default")

	// Simulate all Valkey replicas ready.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	sts.Status.ReadyReplicas = 3
	require.NoError(t, c.Status().Update(context.Background(), sts))

	// Simulate all Sentinel replicas ready.
	sentinelSts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test-sentinel", Namespace: "default"}, sentinelSts))
	sentinelSts.Status.ReadyReplicas = 3
	require.NoError(t, c.Status().Update(context.Background(), sentinelSts))

	// Reconcile should detect cluster health check failure.
	result := reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	assert.Equal(t, vkov1.ValkeyPhaseError, v.Status.Phase)
	assert.Contains(t, v.Status.Message, "Cluster health check failed")
	assert.Contains(t, v.Status.Message, "no master found")

	// Must requeue so transient errors are retried.
	assert.NotZero(t, result.RequeueAfter, "Error phase must trigger a requeue")
}

func TestReconcile_HA_Syncing_WhenReplicationInProgress(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	// Inject cluster state with incomplete replication sync.
	r.InstanceChecker = &mockInstanceChecker{
		clusterState: &health.ClusterState{
			MasterPod:          "test-0",
			ReadyReplicas:      1,
			TotalReplicas:      2,
			AllSynced:          false,
			SentinelMonitoring: true,
		},
	}

	// First reconcile creates resources.
	reconcileOnce(t, r, "test", "default")

	// Simulate all Valkey replicas ready.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	sts.Status.ReadyReplicas = 3
	require.NoError(t, c.Status().Update(context.Background(), sts))

	// Simulate all Sentinel replicas ready.
	sentinelSts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test-sentinel", Namespace: "default"}, sentinelSts))
	sentinelSts.Status.ReadyReplicas = 3
	require.NoError(t, c.Status().Update(context.Background(), sentinelSts))

	// Reconcile should report Syncing.
	result := reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	assert.Equal(t, vkov1.ValkeyphaseSyncing, v.Status.Phase)
	assert.Contains(t, v.Status.Message, "Replication syncing")
	assert.Equal(t, "test-0", v.Status.MasterPod)

	// Must requeue so the controller retries until sync completes.
	assert.NotZero(t, result.RequeueAfter, "Syncing phase must trigger a requeue")
}

// --- Owner References ---

func TestReconcile_SetsOwnerReferences(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Check ConfigMap owner reference.
	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-config", Namespace: "default",
	}, cm)
	require.NoError(t, err)
	assert.Len(t, cm.OwnerReferences, 1)
	assert.Equal(t, "test", cm.OwnerReferences[0].Name)

	// Check headless Service owner reference.
	svc := &corev1.Service{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name: "test-headless", Namespace: "default",
	}, svc)
	require.NoError(t, err)
	assert.Len(t, svc.OwnerReferences, 1)

	// Check StatefulSet owner reference.
	sts := &appsv1.StatefulSet{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)
	require.NoError(t, err)
	assert.Len(t, sts.OwnerReferences, 1)
}

// --- Different Namespace ---

func TestReconcile_CustomNamespace(t *testing.T) {
	v := newTestValkey("test", "production")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "production")

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-config", Namespace: "production",
	}, cm)
	require.NoError(t, err)
	assert.Equal(t, "production", cm.Namespace)
}

// --- Full Standalone Configuration ---

func TestReconcile_FullStandaloneSetup(t *testing.T) {
	v := newTestValkey("standalone", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		v.Spec.Image = "valkey/valkey:8.0"
		v.Spec.PodLabels = map[string]string{"custom": "label"}
		v.Spec.PodAnnotations = map[string]string{"custom/annotation": "true"}
		v.Spec.Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "standalone", "default")

	// All resources should exist.
	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "standalone-config", Namespace: "default"}, cm))

	headlessSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "standalone-headless", Namespace: "default"}, headlessSvc))

	clientSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "standalone", Namespace: "default"}, clientSvc))

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "standalone", Namespace: "default"}, sts))

	// Verify custom labels and annotations on pod template.
	assert.Equal(t, "label", sts.Spec.Template.Labels["custom"])
	assert.Equal(t, "true", sts.Spec.Template.Annotations["custom/annotation"])

	// Verify resources.
	assert.Equal(t, resource.MustParse("500m"), sts.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU])
}

// --- HA Mode (Sentinel) ---

func TestReconcile_HA_CreatesSentinelConfigMap(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel-config", Namespace: "default",
	}, cm)

	require.NoError(t, err)
	assert.Contains(t, cm.Data, "sentinel.conf")
	assert.Contains(t, cm.Data["sentinel.conf"], "sentinel monitor test")
}

func TestReconcile_HA_CreatesReplicaConfigMap(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-replica-config", Namespace: "default",
	}, cm)

	require.NoError(t, err)
	assert.Contains(t, cm.Data, builder.ValkeyConfigKey)
	assert.Contains(t, cm.Data[builder.ValkeyConfigKey], "replicaof")
}

func TestReconcile_HA_CreatesSentinelHeadlessService(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel-headless", Namespace: "default",
	}, svc)

	require.NoError(t, err)
	assert.Equal(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.True(t, svc.Spec.PublishNotReadyAddresses)
	require.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(26379), svc.Spec.Ports[0].Port)
}

func TestReconcile_HA_CreatesSentinelStatefulSet(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel", Namespace: "default",
	}, sts)

	require.NoError(t, err)
	assert.Equal(t, int32(3), *sts.Spec.Replicas)
	assert.Equal(t, "test-sentinel-headless", sts.Spec.ServiceName)
	assert.Equal(t, "valkey/valkey:8.0", sts.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, "sentinel", sts.Spec.Template.Labels["app.kubernetes.io/component"])
}

func TestReconcile_HA_ValkeyStatefulSetHasInitContainer(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)

	require.NoError(t, err)
	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1)
	assert.Equal(t, "init-config-selector", sts.Spec.Template.Spec.InitContainers[0].Name)
}

func TestReconcile_HA_AllResourcesCreated(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
			PodLabels: map[string]string{
				"app": "sentinel",
			},
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "ha", "default")

	// Valkey resources.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-config", Namespace: "default"}, &corev1.ConfigMap{}))
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-replica-config", Namespace: "default"}, &corev1.ConfigMap{}))
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-headless", Namespace: "default"}, &corev1.Service{}))
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha", Namespace: "default"}, &corev1.Service{}))
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha", Namespace: "default"}, &appsv1.StatefulSet{}))

	// Sentinel resources.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-sentinel-config", Namespace: "default"}, &corev1.ConfigMap{}))
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-sentinel-headless", Namespace: "default"}, &corev1.Service{}))
	sentinelSts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-sentinel", Namespace: "default"}, sentinelSts))

	// Verify sentinel custom labels.
	assert.Equal(t, "sentinel", sentinelSts.Spec.Template.Labels["app"])
}

func TestReconcile_HA_SentinelOwnerReferences(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Sentinel ConfigMap.
	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test-sentinel-config", Namespace: "default"}, cm))
	assert.Len(t, cm.OwnerReferences, 1)
	assert.Equal(t, "test", cm.OwnerReferences[0].Name)

	// Sentinel headless Service.
	svc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test-sentinel-headless", Namespace: "default"}, svc))
	assert.Len(t, svc.OwnerReferences, 1)

	// Sentinel StatefulSet.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test-sentinel", Namespace: "default"}, sts))
	assert.Len(t, sts.OwnerReferences, 1)
}

func TestReconcile_HA_Idempotent(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, _ := newTestReconciler(v)

	// Reconcile multiple times — should not error.
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
}

func TestReconcile_StandaloneDoesNotCreateSentinel(t *testing.T) {
	v := newTestValkey("standalone", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "standalone", "default")

	// No sentinel resources should exist.
	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "standalone-sentinel-config", Namespace: "default"}, cm)
	assert.True(t, apierrors.IsNotFound(err))

	svc := &corev1.Service{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "standalone-sentinel-headless", Namespace: "default"}, svc)
	assert.True(t, apierrors.IsNotFound(err))

	sts := &appsv1.StatefulSet{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "standalone-sentinel", Namespace: "default"}, sts)
	assert.True(t, apierrors.IsNotFound(err))

	// No replica configmap either.
	err = c.Get(context.Background(), types.NamespacedName{Name: "standalone-replica-config", Namespace: "default"}, cm)
	assert.True(t, apierrors.IsNotFound(err))
}

// --- Auth Tests ---

func TestReconcile_Auth_StatefulSetHasEnvVar(t *testing.T) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
		},
	}
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})
	r, c := newTestReconciler(v, authSecret)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)

	require.NoError(t, err)
	container := sts.Spec.Template.Spec.Containers[0]

	// Should have the auth env var.
	require.Len(t, container.Env, 1)
	assert.Equal(t, builder.AuthSecretEnvName, container.Env[0].Name)
	require.NotNil(t, container.Env[0].ValueFrom)
	require.NotNil(t, container.Env[0].ValueFrom.SecretKeyRef)
	assert.Equal(t, "my-secret", container.Env[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "password", container.Env[0].ValueFrom.SecretKeyRef.Key)
}

func TestReconcile_Auth_CommandHasAuthFlags(t *testing.T) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
		},
	}
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})
	r, c := newTestReconciler(v, authSecret)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)

	require.NoError(t, err)
	container := sts.Spec.Template.Spec.Containers[0]

	// Command should use shell with auth flags.
	assert.Equal(t, "sh", container.Command[0])
	assert.Contains(t, container.Command[2], "--requirepass")
	assert.Contains(t, container.Command[2], "--masterauth")
}

func TestReconcile_Auth_ConfigMapNoPassword(t *testing.T) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
		},
	}
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})
	r, c := newTestReconciler(v, authSecret)

	reconcileOnce(t, r, "test", "default")

	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-config", Namespace: "default",
	}, cm)

	require.NoError(t, err)
	// The password must NOT appear in the ConfigMap.
	assert.NotContains(t, cm.Data[builder.ValkeyConfigKey], "supersecret")
	assert.NotContains(t, cm.Data[builder.ValkeyConfigKey], "my-secret")
	// But auth section should be present.
	assert.Contains(t, cm.Data[builder.ValkeyConfigKey], "# Auth")
}

func TestReconcile_Auth_ProbeHasAuth(t *testing.T) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
		},
	}
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})
	r, c := newTestReconciler(v, authSecret)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)

	require.NoError(t, err)
	container := sts.Spec.Template.Spec.Containers[0]

	// Readiness probe should use auth.
	require.NotNil(t, container.ReadinessProbe)
	require.NotNil(t, container.ReadinessProbe.Exec)
	probeCmd := container.ReadinessProbe.Exec.Command
	assert.Equal(t, "sh", probeCmd[0])
	assert.Contains(t, probeCmd[2], "-a")
	assert.Contains(t, probeCmd[2], "$VALKEY_PASSWORD")
}

func TestReconcile_Auth_HA_SentinelConfigHasAuth(t *testing.T) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
		},
	}
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v, authSecret)

	reconcileOnce(t, r, "test", "default")

	// Sentinel ConfigMap should have auth placeholder.
	cm := &corev1.ConfigMap{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel-config", Namespace: "default",
	}, cm)

	require.NoError(t, err)
	assert.Contains(t, cm.Data["sentinel.conf"], "sentinel auth-pass test %VALKEY_PASSWORD%")
	// The actual password should NOT be in the ConfigMap.
	assert.NotContains(t, cm.Data["sentinel.conf"], "supersecret")
}

func TestReconcile_Auth_HA_SentinelStatefulSetHasAuthEnv(t *testing.T) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("supersecret"),
		},
	}
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, c := newTestReconciler(v, authSecret)

	reconcileOnce(t, r, "test", "default")

	sentinelSts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel", Namespace: "default",
	}, sentinelSts)

	require.NoError(t, err)

	// Sentinel init container should have the auth env var.
	require.Len(t, sentinelSts.Spec.Template.Spec.InitContainers, 1)
	initContainer := sentinelSts.Spec.Template.Spec.InitContainers[0]
	require.Len(t, initContainer.Env, 1)
	assert.Equal(t, builder.AuthSecretEnvName, initContainer.Env[0].Name)
	assert.Equal(t, "my-secret", initContainer.Env[0].ValueFrom.SecretKeyRef.Name)
}

func TestReconcile_Auth_WithoutAuth_NoEnvVars(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts)

	require.NoError(t, err)
	container := sts.Spec.Template.Spec.Containers[0]

	// No env vars.
	assert.Empty(t, container.Env)

	// Direct command (no shell wrapper).
	assert.Equal(t, "valkey-server", container.Command[0])

	// Probe should be direct (no shell).
	assert.Equal(t, "valkey-cli", container.ReadinessProbe.Exec.Command[0])
}

// --- FindValkeyForSecret ---

func TestFindValkeyForSecret_MatchingSecret(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})
	r, _ := newTestReconciler(v)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
	}

	requests := r.findValkeyForSecret(context.Background(), secret)

	require.Len(t, requests, 1)
	assert.Equal(t, "test", requests[0].Name)
	assert.Equal(t, "default", requests[0].Namespace)
}

func TestFindValkeyForSecret_NonMatchingSecret(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})
	r, _ := newTestReconciler(v)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-secret",
			Namespace: "default",
		},
	}

	requests := r.findValkeyForSecret(context.Background(), secret)

	assert.Empty(t, requests)
}

func TestFindValkeyForSecret_NoAuth(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-secret",
			Namespace: "default",
		},
	}

	requests := r.findValkeyForSecret(context.Background(), secret)

	assert.Empty(t, requests)
}

func TestFindValkeyForSecret_MultipleValkeys(t *testing.T) {
	v1 := newTestValkey("v1", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "shared-secret",
			SecretPasswordKey: "password",
		}
	})
	v2 := newTestValkey("v2", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "shared-secret",
			SecretPasswordKey: "password",
		}
	})
	v3 := newTestValkey("v3", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "other-secret",
			SecretPasswordKey: "password",
		}
	})
	r, _ := newTestReconciler(v1, v2, v3)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-secret",
			Namespace: "default",
		},
	}

	requests := r.findValkeyForSecret(context.Background(), secret)

	// Only v1 and v2 reference shared-secret.
	assert.Len(t, requests, 2)
}

// --- NetworkPolicy Reconciliation ---

func TestReconcile_CreatesValkeyNetworkPolicy(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	np := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: builder.NetworkPolicyName(v), Namespace: "default",
	}, np)

	require.NoError(t, err)
	assert.Equal(t, "test", np.Name)
	assert.Equal(t, "valkey", np.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"])
}

func TestReconcile_CreatesNetworkPolicies_HA(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Valkey NetworkPolicy.
	valkeyNP := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: builder.NetworkPolicyName(v), Namespace: "default",
	}, valkeyNP)
	require.NoError(t, err)
	assert.Len(t, valkeyNP.Spec.Ingress[0].From, 2, "should allow from Valkey and Sentinel")

	// Sentinel NetworkPolicy.
	sentinelNP := &networkingv1.NetworkPolicy{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name: builder.SentinelNetworkPolicyName(v), Namespace: "default",
	}, sentinelNP)
	require.NoError(t, err)
	assert.Equal(t, "sentinel", sentinelNP.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"])
}

func TestReconcile_NoNetworkPolicyWhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	np := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: builder.NetworkPolicyName(v), Namespace: "default",
	}, np)

	assert.True(t, apierrors.IsNotFound(err), "NetworkPolicy should not be created when disabled")
}

func TestReconcile_NetworkPolicy_WithNamePrefix(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{
			Enabled:    true,
			NamePrefix: "my-prefix",
		}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	np := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "my-prefix-test", Namespace: "default",
	}, np)

	require.NoError(t, err)
	assert.Equal(t, "my-prefix-test", np.Name)
}

func TestReconcile_NetworkPolicy_NoSentinelPolicyWithoutSentinel(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Sentinel NetworkPolicy should NOT be created.
	np := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: builder.SentinelNetworkPolicyName(v), Namespace: "default",
	}, np)

	assert.True(t, apierrors.IsNotFound(err), "Sentinel NetworkPolicy should not exist without Sentinel")
}
