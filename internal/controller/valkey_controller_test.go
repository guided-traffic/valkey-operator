package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
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
	pingErr           error
	clusterState      *health.ClusterState
	replicationInfoFn func(podName string) (*valkeyclient.ReplicationInfo, error)
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

func (m *mockInstanceChecker) GetReplicationInfo(_ context.Context, _ *vkov1.Valkey, podName string) (*valkeyclient.ReplicationInfo, error) {
	if m.replicationInfoFn != nil {
		return m.replicationInfoFn(podName)
	}
	// Default: return an error so that collectPodStates falls back to pod labels
	// for master detection. Tests that need sync-check behaviour should set
	// replicationInfoFn explicitly.
	return nil, fmt.Errorf("mock: no replication info for %s", podName)
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
		OperatorImage:   "ghcr.io/guided-traffic/valkey-operator:test",
		// Redirect all Valkey client connections to localhost so unit tests
		// get instant "connection refused" instead of DNS/TCP timeouts.
		// The original port is preserved so tests that verify port selection
		// (e.g., TLS vs plain) still see the expected port in error messages.
		NewValkeyClientFn: func(addr, password string, tlsConfig *tls.Config) *valkeyclient.Client {
			_, port, _ := net.SplitHostPort(addr)
			if port == "" {
				port = "1"
			}
			return valkeyclient.New("127.0.0.1:" + port)
		},
	}, fakeClient
}

func newTestValkey(name, ns string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			// A real CR always has one, and ownership checks compare it. Without a
			// UID here, metav1.IsControlledBy matches an ownerReference whose UID is
			// also empty, so an ownership test would pass without testing anything.
			UID: types.UID(name + "-" + ns + "-uid"),
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

// TestReconcile_DeletionTimestamp verifies that no child resources are created or
// updated when the Valkey resource is being deleted (DeletionTimestamp is set).
// This prevents a reboot loop on partially provisioned clusters during deletion.
func TestReconcile_DeletionTimestamp_SkipsReconciliation(t *testing.T) {
	now := metav1.Now()
	v := newTestValkey("deleting", "default", func(v *vkov1.Valkey) {
		v.DeletionTimestamp = &now
		// A finalizer is required for the object to remain in "terminating" state.
		v.Finalizers = []string{"foregroundDeletion"}
	})
	r, c := newTestReconciler(v)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "deleting", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// No StatefulSet should have been created.
	sts := &appsv1.StatefulSet{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "deleting", Namespace: "default"}, sts)
	assert.True(t, apierrors.IsNotFound(err), "expected no StatefulSet to be created during deletion")

	// No ConfigMap should have been created.
	cm := &corev1.ConfigMap{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "deleting-config", Namespace: "default"}, cm)
	assert.True(t, apierrors.IsNotFound(err), "expected no ConfigMap to be created during deletion")
}

// TestReconcile_DeletionTimestamp_PartiallyProvisioned verifies that a Valkey that
// was never fully provisioned (phase = Provisioning) does not keep creating pods
// when it carries a deletionTimestamp.
func TestReconcile_DeletionTimestamp_PartiallyProvisioned(t *testing.T) {
	now := metav1.Now()
	v := newTestValkey("stuck", "default", func(v *vkov1.Valkey) {
		v.DeletionTimestamp = &now
		v.Finalizers = []string{"foregroundDeletion"}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = "Waiting for HA cluster pods to become ready"
	})
	r, c := newTestReconciler(v)

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "stuck", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Sentinel StatefulSet must NOT be created.
	sentinelSts := &appsv1.StatefulSet{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "stuck-sentinel", Namespace: "default"}, sentinelSts)
	assert.True(t, apierrors.IsNotFound(err), "expected no Sentinel StatefulSet during deletion of partially provisioned cluster")

	// Valkey StatefulSet must NOT be created.
	sts := &appsv1.StatefulSet{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "stuck", Namespace: "default"}, sts)
	assert.True(t, apierrors.IsNotFound(err), "expected no Valkey StatefulSet during deletion of partially provisioned cluster")

	// Phase must not have been overwritten.
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "stuck", Namespace: "default"}, updated))
	assert.Equal(t, vkov1.ValkeyPhaseProvisioning, updated.Status.Phase, "phase must remain unchanged during deletion")
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

func TestReconcile_CreatesRWService(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-rw", Namespace: "default",
	}, svc)

	require.NoError(t, err)
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(6379), svc.Spec.Ports[0].Port)
	// -rw service must select master pods.
	assert.Equal(t, "master", svc.Spec.Selector["vko.gtrfc.com/instanceRole"])
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

func TestReconcile_MultiReplica_CreatesAllService(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-all", Namespace: "default",
	}, svc)

	require.NoError(t, err)
	assert.Equal(t, int32(6379), svc.Spec.Ports[0].Port)
	_, hasRole := svc.Spec.Selector["vko.gtrfc.com/instanceRole"]
	assert.False(t, hasRole, "-all service must not filter by role")
}

func TestReconcile_MultiReplica_CreatesReadOnlyService(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-r", Namespace: "default",
	}, svc)

	require.NoError(t, err)
	assert.Equal(t, "replica", svc.Spec.Selector["vko.gtrfc.com/instanceRole"])
}

func TestReconcile_Standalone_DoesNotCreateAllOrReadOnlyService(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	allSvc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-all", Namespace: "default",
	}, allSvc)
	assert.True(t, apierrors.IsNotFound(err), "-all service should not exist for standalone")

	rSvc := &corev1.Service{}
	err = c.Get(context.Background(), types.NamespacedName{
		Name: "test-r", Namespace: "default",
	}, rSvc)
	assert.True(t, apierrors.IsNotFound(err), "-r service should not exist for standalone")
}

// --- Sidecar RBAC ---

func TestReconcile_CreatesSidecarServiceAccount(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sa := &corev1.ServiceAccount{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, sa)

	require.NoError(t, err)
	assert.Equal(t, "test-sidecar", sa.Name)
}

func TestReconcile_CreatesSidecarRole(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	role := &rbacv1.Role{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, role)

	require.NoError(t, err)
	require.Len(t, role.Rules, 1)
	assert.Contains(t, role.Rules[0].Verbs, "patch")
	assert.Contains(t, role.Rules[0].Resources, "pods")
}

func TestReconcile_CreatesSidecarRoleBinding(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	rb := &rbacv1.RoleBinding{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, rb)

	require.NoError(t, err)
	assert.Equal(t, "test-sidecar", rb.RoleRef.Name)
	require.Len(t, rb.Subjects, 1)
	assert.Equal(t, "test-sidecar", rb.Subjects[0].Name)
}

func TestReconcile_RBAC_Idempotent(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	// Multiple reconciles must not error on RBAC resources.
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
}

func TestReconcile_UpdatesServiceAccount_OnLabelDrift(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// First reconcile creates the ServiceAccount.
	reconcileOnce(t, r, "test", "default")

	// Manually corrupt the labels to simulate drift.
	sa := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, sa))
	sa.Labels = map[string]string{"stale": "label"}
	require.NoError(t, c.Update(context.Background(), sa))

	// Second reconcile must restore the labels.
	reconcileOnce(t, r, "test", "default")

	updated := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, updated))
	assert.Equal(t, "vko.gtrfc.com", updated.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "test", updated.Labels["app.kubernetes.io/instance"])
	_, hasStale := updated.Labels["stale"]
	assert.False(t, hasStale, "stale label should be removed after reconcile")
}

func TestReconcile_UpdatesRoleBinding_OnSubjectsDrift(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// First reconcile creates the RoleBinding.
	reconcileOnce(t, r, "test", "default")

	// Manually corrupt the subjects to simulate drift.
	rb := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, rb))
	rb.Subjects = []rbacv1.Subject{{Kind: "ServiceAccount", Name: "wrong-name", Namespace: "default"}}
	require.NoError(t, c.Update(context.Background(), rb))

	// Second reconcile must restore the subjects.
	reconcileOnce(t, r, "test", "default")

	updated := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, updated))
	require.Len(t, updated.Subjects, 1)
	assert.Equal(t, "test-sidecar", updated.Subjects[0].Name)
}

func TestReconcile_UpdatesRoleBinding_OnLabelDrift(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// First reconcile creates the RoleBinding.
	reconcileOnce(t, r, "test", "default")

	// Manually corrupt the labels to simulate drift.
	rb := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, rb))
	rb.Labels = map[string]string{"stale": "label"}
	require.NoError(t, c.Update(context.Background(), rb))

	// Second reconcile must restore the labels.
	reconcileOnce(t, r, "test", "default")

	updated := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, updated))
	assert.Equal(t, "vko.gtrfc.com", updated.Labels["app.kubernetes.io/managed-by"])
	_, hasStale := updated.Labels["stale"]
	assert.False(t, hasStale, "stale label should be removed after reconcile")
}

func TestReconcile_RecreatesRoleBinding_OnRoleRefChange(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// First reconcile creates the RoleBinding.
	reconcileOnce(t, r, "test", "default")

	// Manually corrupt the RoleRef to simulate an operator upgrade that changed it.
	// RoleRef is immutable, so we delete and recreate with the wrong value using the fake client
	// by directly applying an object with wrong RoleRef (fake client allows this).
	rb := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, rb))
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     "wrong-role",
	}
	require.NoError(t, c.Update(context.Background(), rb))

	// Second reconcile must delete the old RoleBinding and recreate it with the correct RoleRef.
	reconcileOnce(t, r, "test", "default")

	recreated := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, recreated))
	assert.Equal(t, "Role", recreated.RoleRef.Kind)
	assert.Equal(t, "test-sidecar", recreated.RoleRef.Name)
}

// --- Legacy Service Cleanup ---

func TestReconcile_DeletesLegacyClientService(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// Pre-create a legacy service owned by this Valkey instance.
	legacySvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "vko.gtrfc.com/v1",
					Kind:       "Valkey",
					Name:       v.Name,
					UID:        v.UID,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 6379}},
		},
	}
	require.NoError(t, c.Create(context.Background(), legacySvc))

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, svc)
	assert.True(t, apierrors.IsNotFound(err), "legacy service 'test' should be deleted")
}

func TestReconcile_DeletesLegacyReadService(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// Pre-create a legacy read service owned by this Valkey instance.
	legacySvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-read",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "vko.gtrfc.com/v1",
					Kind:       "Valkey",
					Name:       v.Name,
					UID:        v.UID,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 6379}},
		},
	}
	require.NoError(t, c.Create(context.Background(), legacySvc))

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-read", Namespace: "default",
	}, svc)
	assert.True(t, apierrors.IsNotFound(err), "legacy service 'test-read' should be deleted")
}

func TestReconcile_DoesNotDeleteUnownedLegacyService(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	// Pre-create a legacy service NOT owned by this Valkey instance.
	unownedSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 6379}},
		},
	}
	require.NoError(t, c.Create(context.Background(), unownedSvc))

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, svc)
	assert.NoError(t, err, "unowned service must not be deleted")
}

// --- Multi-port Services (TLS + allowUnencrypted) ---

func TestReconcile_TLS_RWServiceUsesPort16379(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.RWServiceName(v), Namespace: "default",
	}, svc))
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(builder.TLSPort), svc.Spec.Ports[0].Port)
	assert.Equal(t, "valkey", svc.Spec.Ports[0].TargetPort.String())
}

func TestReconcile_AllowUnencrypted_ExtraPortOnExistingServices(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, AllowUnencrypted: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// -rw: two ports — TLS primary, plain secondary.
	rwSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.RWServiceName(v), Namespace: "default",
	}, rwSvc))
	assert.Len(t, rwSvc.Spec.Ports, 2)
	assert.Equal(t, int32(builder.TLSPort), rwSvc.Spec.Ports[0].Port)
	assert.Equal(t, "valkey-plain", rwSvc.Spec.Ports[1].Name)
	assert.Equal(t, int32(builder.ValkeyPort), rwSvc.Spec.Ports[1].Port)

	// -all: same.
	allSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.AllServiceName(v), Namespace: "default",
	}, allSvc))
	assert.Len(t, allSvc.Spec.Ports, 2)
	_, hasRole := allSvc.Spec.Selector["vko.gtrfc.com/instanceRole"]
	assert.False(t, hasRole, "-all service must not filter by role")

	// -r: same.
	rSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.ReadOnlyServiceName(v), Namespace: "default",
	}, rSvc))
	assert.Len(t, rSvc.Spec.Ports, 2)
	assert.Equal(t, "replica", rSvc.Spec.Selector["vko.gtrfc.com/instanceRole"])
}

func TestReconcile_AllowUnencrypted_RemovedPort_WhenFlagDisabled(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, AllowUnencrypted: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Verify two ports after first reconcile.
	rwSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.RWServiceName(v), Namespace: "default",
	}, rwSvc))
	assert.Len(t, rwSvc.Spec.Ports, 2, "-rw service must have 2 ports when allowUnencrypted")

	// Disable the flag.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v))
	v.Spec.TLS.AllowUnencrypted = false
	require.NoError(t, c.Update(context.Background(), v))

	reconcileOnce(t, r, "test", "default")

	updated := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.RWServiceName(v), Namespace: "default",
	}, updated))
	assert.Len(t, updated.Spec.Ports, 1, "-rw service must have only 1 port after flag removed")
	assert.Equal(t, int32(builder.TLSPort), updated.Spec.Ports[0].Port)
}

func TestReconcile_NoTLS_ServiceUsesSinglePort6379(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.RWServiceName(v), Namespace: "default",
	}, svc))
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(builder.ValkeyPort), svc.Spec.Ports[0].Port)
}

func TestReconcile_SentinelTLS_HeadlessServiceUsesPort36379(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sentinelSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel-headless", Namespace: "default",
	}, sentinelSvc))
	assert.Len(t, sentinelSvc.Spec.Ports, 1)
	assert.Equal(t, int32(builder.SentinelTLSPort), sentinelSvc.Spec.Ports[0].Port)
}

func TestReconcile_SentinelAllowUnencrypted_ExtraPortOnHeadlessService(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, AllowUnencrypted: true}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3, AllowUnencrypted: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sentinelSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel-headless", Namespace: "default",
	}, sentinelSvc))
	assert.Len(t, sentinelSvc.Spec.Ports, 2)
	assert.Equal(t, int32(builder.SentinelTLSPort), sentinelSvc.Spec.Ports[0].Port)
	assert.Equal(t, "sentinel-plain", sentinelSvc.Spec.Ports[1].Name)
	assert.Equal(t, int32(builder.SentinelPort), sentinelSvc.Spec.Ports[1].Port)
}

func TestReconcile_Sentinel_TLSOnly_NoExtraPortOnHeadlessService(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3, AllowUnencrypted: false}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sentinelSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sentinel-headless", Namespace: "default",
	}, sentinelSvc))
	assert.Len(t, sentinelSvc.Spec.Ports, 1, "sentinel headless must have only TLS port when allowUnencrypted=false")
	assert.Equal(t, int32(builder.SentinelTLSPort), sentinelSvc.Spec.Ports[0].Port)
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

// --- Unified Certificate migration ---

// newLegacySentinelCert builds an unstructured cert-manager Certificate matching
// what the operator created in split-cert mode, so migration tests can stage one.
// newLegacySentinelCert builds the legacy Sentinel Certificate as the operator
// itself would have written it: controller ownerReference on the CR and
// spec.secretName pointing at its own name. That pair is the in-pass provenance
// proof the cleanup consumes, so a Certificate built any other way is foreign by
// construction — see newForeignLegacySentinelCert.
func newLegacySentinelCert(v *vkov1.Valkey, name string) *unstructured.Unstructured {
	c := newForeignLegacySentinelCert(name, v.Namespace)
	ownerRef := builder.CertificateOwnerRef(v)
	blockOwnerDeletion := true
	isController := true
	ownerRef.BlockOwnerDeletion = &blockOwnerDeletion
	ownerRef.Controller = &isController
	c.SetOwnerReferences([]metav1.OwnerReference{ownerRef})
	return c
}

// newForeignLegacySentinelCert builds a Certificate that merely carries the legacy
// name — no ownerReference to any Valkey. Models the ADR 0006 D4-D11 collision: a name the
// operator would clean up, on an object it never created.
func newForeignLegacySentinelCert(name, namespace string) *unstructured.Unstructured {
	c := &unstructured.Unstructured{}
	c.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})
	c.SetName(name)
	c.SetNamespace(namespace)
	c.Object["spec"] = map[string]interface{}{"secretName": name}
	return c
}

// newLegacySentinelSecret builds the Secret as cert-manager issues it: type
// kubernetes.io/tls plus the cert-manager.io/certificate-name annotation naming
// the Certificate that produced it. Verified against cert-manager v1.21.1 — the
// annotation carries the CERTIFICATE name, which for the legacy Sentinel material
// equals the Secret name because SentinelCertificateName and SentinelTLSSecretName
// derive the same string.
func newLegacySentinelSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{certManagerCertificateNameAnnotation: name},
			Labels:      map[string]string{"controller.cert-manager.io/fao": "true"},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")},
	}
}

// stagedSentinelStatefulSet builds a Sentinel StatefulSet that mounts the named
// TLS Secret on volume "tls" with a fully-rolled-out status (observedGeneration
// matches generation, updateRevision set). Used as a base for migration tests;
// callers add Pods that carry the matching revision label to simulate "rollout
// complete" or supply an older label to simulate "rollout in progress".
func stagedSentinelStatefulSet(v *vkov1.Valkey, name, tlsSecretName string) *appsv1.StatefulSet {
	replicas := int32(3)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "iam", Generation: 1},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: builder.TLSVolumeName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{SecretName: tlsSecretName},
							},
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 1,
			UpdateRevision:     "rev-new",
			CurrentRevision:    "rev-new",
		},
	}
	// The ADR 0020 guards treat an un-owned StatefulSet as absent.
	controllerRefTo(v, sts)
	return sts
}

// readySentinelPods returns N ready Sentinel pods all stamped with the
// "rev-new" controller-revision-hash that stagedSentinelStatefulSet exposes,
// used to model a rolled-out fleet.
func readySentinelPods(stsName string, count int32) []client.Object {
	pods := make([]client.Object, 0, count)
	for i := int32(0); i < count; i++ {
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%d", stsName, i),
				Namespace: "iam",
				Labels:    map[string]string{appsv1.StatefulSetRevisionLabel: "rev-new"},
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				},
			},
		})
	}
	return pods
}

func newTestValkeyUnified() *vkov1.Valkey {
	return newTestValkey("oauth2-valkey", "iam", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:            true,
			UnifiedCertificate: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})
}

func TestReconcileLegacySentinelCleanup_Noop_WhenNotUnified(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})
	legacyCert := newLegacySentinelCert(v, builder.SentinelCertificateName(v))
	legacySecret := newLegacySentinelSecret(builder.SentinelCertificateName(v), "default")
	r, c := newTestReconciler(v, legacyCert, legacySecret)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	// Legacy resources must remain untouched in split-cert mode.
	gotCert := &unstructured.Unstructured{}
	gotCert.SetGroupVersionKind(legacyCert.GroupVersionKind())
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyCert.GetName(), Namespace: "default"}, gotCert))
	gotSecret := &corev1.Secret{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacySecret.Name, Namespace: "default"}, gotSecret))
}

func TestReconcileLegacySentinelCleanup_Defers_WhenSTSStillMountsLegacySecret(t *testing.T) {
	v := newTestValkeyUnified()
	legacyCertName := builder.SentinelCertificateName(v) // oauth2-valkey-sentinel-tls
	legacyCert := newLegacySentinelCert(v, legacyCertName)
	legacySecret := newLegacySentinelSecret(legacyCertName, "iam")
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	// STS still points at legacy Secret AND pods still on the old revision.
	sts := stagedSentinelStatefulSet(v, stsName, legacyCertName)
	pods := readySentinelPods(stsName, 3)
	objs := append([]client.Object{v, legacyCert, legacySecret, sts}, pods...)
	r, c := newTestReconciler(objs...)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	assertLegacyCertExists(t, c, legacyCert)
	assertLegacySecretExists(t, c, legacyCertName)
}

func TestReconcileLegacySentinelCleanup_Defers_WhenAnyPodOnOldRevision(t *testing.T) {
	v := newTestValkeyUnified()
	legacyCertName := builder.SentinelCertificateName(v)
	unifiedSecretName := builder.ValkeyTLSSecretName(v)
	legacyCert := newLegacySentinelCert(v, legacyCertName)
	legacySecret := newLegacySentinelSecret(legacyCertName, "iam")
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, unifiedSecretName)
	// Two pods rolled to the new revision, one still on the old one.
	pods := readySentinelPods(stsName, 2)
	pods = append(pods, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      stsName + "-2",
			Namespace: "iam",
			Labels:    map[string]string{appsv1.StatefulSetRevisionLabel: "rev-old"},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	})
	objs := append([]client.Object{v, legacyCert, legacySecret, sts}, pods...)
	r, c := newTestReconciler(objs...)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	assertLegacyCertExists(t, c, legacyCert)
	assertLegacySecretExists(t, c, legacyCertName)
}

func TestReconcileLegacySentinelCleanup_Defers_WhenAnyPodNotReady(t *testing.T) {
	v := newTestValkeyUnified()
	legacyCertName := builder.SentinelCertificateName(v)
	unifiedSecretName := builder.ValkeyTLSSecretName(v)
	legacyCert := newLegacySentinelCert(v, legacyCertName)
	legacySecret := newLegacySentinelSecret(legacyCertName, "iam")
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, unifiedSecretName)
	pods := readySentinelPods(stsName, 2)
	pods = append(pods, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      stsName + "-2",
			Namespace: "iam",
			Labels:    map[string]string{appsv1.StatefulSetRevisionLabel: "rev-new"},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
		},
	})
	objs := append([]client.Object{v, legacyCert, legacySecret, sts}, pods...)
	r, c := newTestReconciler(objs...)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	assertLegacyCertExists(t, c, legacyCert)
	assertLegacySecretExists(t, c, legacyCertName)
}

func TestReconcileLegacySentinelCleanup_Defers_WhenObservedGenerationStale(t *testing.T) {
	v := newTestValkeyUnified()
	legacyCertName := builder.SentinelCertificateName(v)
	unifiedSecretName := builder.ValkeyTLSSecretName(v)
	legacyCert := newLegacySentinelCert(v, legacyCertName)
	legacySecret := newLegacySentinelSecret(legacyCertName, "iam")
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, unifiedSecretName)
	// Spec-update has happened (Generation bumped) but STS controller has not yet
	// observed it — pods may still be on the previous revision.
	sts.Generation = 2
	pods := readySentinelPods(stsName, 3)
	objs := append([]client.Object{v, legacyCert, legacySecret, sts}, pods...)
	r, c := newTestReconciler(objs...)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	assertLegacyCertExists(t, c, legacyCert)
	assertLegacySecretExists(t, c, legacyCertName)
}

func TestReconcileLegacySentinelCleanup_Deletes_WhenAllPodsOnNewRevision(t *testing.T) {
	v := newTestValkeyUnified()
	legacyCertName := builder.SentinelCertificateName(v)
	unifiedSecretName := builder.ValkeyTLSSecretName(v)
	legacyCert := newLegacySentinelCert(v, legacyCertName)
	legacySecret := newLegacySentinelSecret(legacyCertName, "iam")
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, unifiedSecretName)
	pods := readySentinelPods(stsName, 3)
	objs := append([]client.Object{v, legacyCert, legacySecret, sts}, pods...)
	r, c := newTestReconciler(objs...)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	gotCert := &unstructured.Unstructured{}
	gotCert.SetGroupVersionKind(legacyCert.GroupVersionKind())
	err := c.Get(context.Background(), types.NamespacedName{Name: legacyCertName, Namespace: "iam"}, gotCert)
	assert.True(t, apierrors.IsNotFound(err), "legacy Certificate should be deleted: %v", err)

	gotSecret := &corev1.Secret{}
	err = c.Get(context.Background(), types.NamespacedName{Name: legacyCertName, Namespace: "iam"}, gotSecret)
	assert.True(t, apierrors.IsNotFound(err), "legacy Secret should be deleted: %v", err)
}

func TestReconcileLegacySentinelCleanup_Deletes_WhenSentinelSTSAbsent(t *testing.T) {
	// Fresh cluster with unified mode and no Sentinel STS yet (or transient gap)
	// — cleanup is safe because there are no pods to mount the legacy Secret.
	v := newTestValkeyUnified()
	legacyCertName := builder.SentinelCertificateName(v)
	legacyCert := newLegacySentinelCert(v, legacyCertName)
	legacySecret := newLegacySentinelSecret(legacyCertName, "iam")
	r, c := newTestReconciler(v, legacyCert, legacySecret)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	gotCert := &unstructured.Unstructured{}
	gotCert.SetGroupVersionKind(legacyCert.GroupVersionKind())
	err := c.Get(context.Background(), types.NamespacedName{Name: legacyCertName, Namespace: "iam"}, gotCert)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestReconcileLegacySentinelCleanup_Idempotent_NotFound(t *testing.T) {
	v := newTestValkeyUnified()
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, builder.ValkeyTLSSecretName(v))
	pods := readySentinelPods(stsName, 3)
	objs := append([]client.Object{v, sts}, pods...)
	r, _ := newTestReconciler(objs...)

	// No legacy resources present; must not error.
	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))
	// Calling twice is also fine.
	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))
}

// TestReconcileLegacySentinelCleanup_FreshInstall_NoDeleteRBAC simulates a
// brand-new cluster with unifiedCertificate=true: no legacy Cert/Secret has
// ever existed. The kube-apiserver evaluates authz BEFORE existence, so a
// Delete on a missing resource against a role lacking the delete verb returns
// 403 Forbidden — not 404 NotFound. The cleanup must therefore GET the
// resource first and skip the Delete entirely when it does not exist, so the
// reconciler does not loop on a phantom RBAC error.
func TestReconcileLegacySentinelCleanup_FreshInstall_NoDeleteRBAC(t *testing.T) {
	v := newTestValkeyUnified()

	// Fake client whose Delete reproduces the apiserver's authz-before-existence
	// behaviour: any Delete attempt is rejected with Forbidden, regardless of
	// whether the target exists. If the cleanup ever reaches Delete, the test
	// fails — proving the GET-first guard skips it for missing resources.
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(v).
		WithStatusSubresource(&vkov1.Valkey{}, &appsv1.StatefulSet{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
				gvk := obj.GetObjectKind().GroupVersionKind()
				return apierrors.NewForbidden(
					schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind},
					obj.GetName(),
					fmt.Errorf("simulated missing delete RBAC"),
				)
			},
		}).
		Build()
	r := &ValkeyReconciler{Client: c, Scheme: s, InstanceChecker: &mockInstanceChecker{}}

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v),
		"cleanup must succeed when no legacy resources exist, even when delete RBAC is missing")
}

func assertLegacyCertExists(t *testing.T, c client.Client, legacyCert *unstructured.Unstructured) {
	t.Helper()
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(legacyCert.GroupVersionKind())
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyCert.GetName(), Namespace: "iam"}, got),
		"legacy Certificate must remain until rollout completes")
}

func assertLegacySecretExists(t *testing.T, c client.Client, name string) {
	t.Helper()
	got := &corev1.Secret{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: "iam"}, got),
		"legacy Secret must remain until rollout completes")
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

	rwSvc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "standalone-rw", Namespace: "default"}, rwSvc))

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
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-rw", Namespace: "default"}, &corev1.Service{}))
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-all", Namespace: "default"}, &corev1.Service{}))
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-r", Namespace: "default"}, &corev1.Service{}))
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

func TestReconcile_MultiReplicaWithoutSentinel_CreatesReplicaConfigMap(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
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

func TestReconcile_MultiReplicaWithoutSentinel_SetsMasterPod(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, c := newTestReconciler(v)

	// First reconcile creates resources.
	reconcileOnce(t, r, "test", "default")

	// Simulate all replicas ready.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	sts.Status.ReadyReplicas = 3
	require.NoError(t, c.Status().Update(context.Background(), sts))

	// Second reconcile should report OK with master pod set.
	reconcileOnce(t, r, "test", "default")

	err := c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v)
	require.NoError(t, err)

	assert.Equal(t, vkov1.ValkeyPhaseOK, v.Status.Phase)
	assert.Equal(t, "test-0", v.Status.MasterPod)
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

// --- Operator Version Annotation ---

// newTestReconcilerWithVersion creates a test reconciler with a specific operator version.
func newTestReconcilerWithVersion(version string, objs ...client.Object) (*ValkeyReconciler, client.Client) {
	r, c := newTestReconciler(objs...)
	r.OperatorVersion = version
	return r, c
}

func TestReconcile_SetsOperatorVersionAnnotation_OnConfigMap(t *testing.T) {
	const version = "1.2.3"
	v := newTestValkey("test", "default")
	r, c := newTestReconcilerWithVersion(version, v)

	reconcileOnce(t, r, "test", "default")

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.ConfigMapName(v), Namespace: "default",
	}, cm))
	assert.Equal(t, version, cm.Annotations[builder.AnnotationOperatorVersion],
		"ConfigMap should carry the operator-version annotation")
}

func TestReconcile_SetsOperatorVersionAnnotation_OnStatefulSet(t *testing.T) {
	const version = "1.2.3"
	v := newTestValkey("test", "default")
	r, c := newTestReconcilerWithVersion(version, v)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts))
	assert.Equal(t, version, sts.Annotations[builder.AnnotationOperatorVersion],
		"StatefulSet should carry the operator-version annotation")
}

func TestReconcile_SetsOperatorVersionAnnotation_OnService(t *testing.T) {
	const version = "1.2.3"
	v := newTestValkey("test", "default")
	r, c := newTestReconcilerWithVersion(version, v)

	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-rw", Namespace: "default",
	}, svc))
	assert.Equal(t, version, svc.Annotations[builder.AnnotationOperatorVersion],
		"Service should carry the operator-version annotation")
}

func TestReconcile_SetsOperatorVersionAnnotation_OnRBAC(t *testing.T) {
	const version = "1.2.3"
	v := newTestValkey("test", "default")
	r, c := newTestReconcilerWithVersion(version, v)

	reconcileOnce(t, r, "test", "default")

	sa := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, sa))
	assert.Equal(t, version, sa.Annotations[builder.AnnotationOperatorVersion],
		"ServiceAccount should carry the operator-version annotation")

	role := &rbacv1.Role{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, role))
	assert.Equal(t, version, role.Annotations[builder.AnnotationOperatorVersion],
		"Role should carry the operator-version annotation")

	rb := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-sidecar", Namespace: "default",
	}, rb))
	assert.Equal(t, version, rb.Annotations[builder.AnnotationOperatorVersion],
		"RoleBinding should carry the operator-version annotation")
}

func TestReconcile_UpdatesOperatorVersionAnnotation_OnConfigMapDrift(t *testing.T) {
	const oldVersion = "1.0.0"
	const newVersion = "1.1.0"

	v := newTestValkey("test", "default")

	// Pre-create the ConfigMap with the old operator version annotation.
	cm := builder.BuildConfigMap(v)
	builder.ApplyOperatorVersion(cm, oldVersion)
	r, c := newTestReconcilerWithVersion(newVersion, v, cm)

	reconcileOnce(t, r, "test", "default")

	updated := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.ConfigMapName(v), Namespace: "default",
	}, updated))
	assert.Equal(t, newVersion, updated.Annotations[builder.AnnotationOperatorVersion],
		"ConfigMap annotation should be updated to the new operator version")
}

func TestReconcile_UpdatesOperatorVersionAnnotation_OnStatefulSetDrift(t *testing.T) {
	const oldVersion = "1.0.0"
	const newVersion = "1.1.0"

	v := newTestValkey("test", "default")

	// Pre-create the StatefulSet with the old operator version annotation via initial reconcile.
	r, c := newTestReconcilerWithVersion(oldVersion, v)
	reconcileOnce(t, r, "test", "default")

	// Upgrade the reconciler to the new version and reconcile again.
	r.OperatorVersion = newVersion
	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts))
	assert.Equal(t, newVersion, sts.Annotations[builder.AnnotationOperatorVersion],
		"StatefulSet annotation should be updated to the new operator version")
}

func TestReconcile_UpdatesOperatorVersionAnnotation_OnServiceDrift(t *testing.T) {
	const oldVersion = "1.0.0"
	const newVersion = "1.1.0"

	v := newTestValkey("test", "default")
	r, c := newTestReconcilerWithVersion(oldVersion, v)
	reconcileOnce(t, r, "test", "default")

	r.OperatorVersion = newVersion
	reconcileOnce(t, r, "test", "default")

	svc := &corev1.Service{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test-rw", Namespace: "default",
	}, svc))
	assert.Equal(t, newVersion, svc.Annotations[builder.AnnotationOperatorVersion],
		"Service annotation should be updated to the new operator version")
}

func TestReconcile_SetsStatusOperatorVersion(t *testing.T) {
	const version = "2.0.0"
	v := newTestValkey("test", "default")
	r, c := newTestReconcilerWithVersion(version, v)

	// Reconcile creates all resources, then updateStatus populates status.operatorVersion.
	// We need the StatefulSet to be "ready" so status proceeds past Provisioning.
	reconcileOnce(t, r, "test", "default")

	// Simulate a ready StatefulSet so status.operatorVersion gets written.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts))
	sts.Status.ReadyReplicas = 1
	sts.Status.Replicas = 1
	require.NoError(t, c.Status().Update(context.Background(), sts))

	reconcileOnce(t, r, "test", "default")

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, updated))
	assert.Equal(t, version, updated.Status.OperatorVersion,
		"status.operatorVersion must reflect the current operator version")
}

func TestReconcile_StatusOperatorVersion_UpdatedOnVersionChange(t *testing.T) {
	const oldVersion = "1.0.0"
	const newVersion = "2.0.0"

	v := newTestValkey("test", "default")
	r, c := newTestReconcilerWithVersion(oldVersion, v)

	// First reconcile with old version.
	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, sts))
	sts.Status.ReadyReplicas = 1
	sts.Status.Replicas = 1
	require.NoError(t, c.Status().Update(context.Background(), sts))

	reconcileOnce(t, r, "test", "default")

	// Upgrade operator version and reconcile again.
	r.OperatorVersion = newVersion
	reconcileOnce(t, r, "test", "default")

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: "test", Namespace: "default",
	}, updated))
	assert.Equal(t, newVersion, updated.Status.OperatorVersion,
		"status.operatorVersion must be updated when the operator version changes")
}

// --- sentinelPassword Tests ---

func TestSentinelPassword_ReturnsEmpty_WhenDisableAuthTrue(t *testing.T) {
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
			Enabled:     true,
			Replicas:    3,
			DisableAuth: true,
		}
	})
	r, _ := newTestReconciler(v, authSecret)

	pwd := r.sentinelPassword(context.Background(), v)
	assert.Equal(t, "", pwd, "sentinel password must be empty when disableAuth is true")
}

func TestSentinelPassword_ReturnsPassword_WhenDisableAuthFalse(t *testing.T) {
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
			Enabled:     true,
			Replicas:    3,
			DisableAuth: false,
		}
	})
	r, _ := newTestReconciler(v, authSecret)

	pwd := r.sentinelPassword(context.Background(), v)
	assert.Equal(t, "supersecret", pwd, "sentinel password must match secret when disableAuth is false")
}

func TestSentinelPassword_ReturnsEmpty_WhenNoAuth(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, _ := newTestReconciler(v)

	pwd := r.sentinelPassword(context.Background(), v)
	assert.Equal(t, "", pwd, "sentinel password must be empty when auth is not configured")
}

// --- Observer Deployment Tests ---

func TestReconcile_CreatesObserverDeployment(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	deploy := &appsv1.Deployment{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: builder.ObserverDeploymentName(v), Namespace: "default",
	}, deploy)

	require.NoError(t, err)
	assert.Equal(t, "test-observer", deploy.Name)
	assert.Equal(t, int32(1), *deploy.Spec.Replicas)
	require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "observer", deploy.Spec.Template.Spec.Containers[0].Name)
}

func TestReconcile_ObserverDeployment_NotCreated_WhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	deploy := &appsv1.Deployment{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-observer", Namespace: "default",
	}, deploy)

	assert.True(t, apierrors.IsNotFound(err), "observer deployment should not exist when observer is disabled")
}

func TestReconcile_ObserverDeployment_Idempotent(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	r, _ := newTestReconciler(v)

	// Multiple reconciles must not error.
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
	reconcileOnce(t, r, "test", "default")
}

func TestReconcile_ObserverDeployment_CleanupOnDisable(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	// First reconcile creates observer deployment.
	reconcileOnce(t, r, "test", "default")

	deploy := &appsv1.Deployment{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: "test-observer", Namespace: "default",
	}, deploy)
	require.NoError(t, err, "observer deployment should exist before disable")

	// Disable observer.
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, updated))
	updated.Spec.Observer.Enabled = false
	require.NoError(t, c.Update(context.Background(), updated))

	// Reconcile again to trigger cleanup.
	reconcileOnce(t, r, "test", "default")

	err = c.Get(context.Background(), types.NamespacedName{
		Name: "test-observer", Namespace: "default",
	}, deploy)
	assert.True(t, apierrors.IsNotFound(err), "observer deployment should be deleted after disable")
}

func TestReconcile_ObserverDeployment_WithNetworkPolicy(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Observer NetworkPolicy should be created.
	np := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: builder.ObserverNetworkPolicyName(v), Namespace: "default",
	}, np)

	require.NoError(t, err)
	assert.Equal(t, "test-observer", np.Name)
}

func TestReconcile_ObserverStatus_FalseWhenNoReadyReplicas(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, updated))

	// Observer deployment was just created, no ready replicas yet.
	if updated.Status.ObserverReady != nil {
		assert.False(t, *updated.Status.ObserverReady, "observer should not be ready when deployment has no ready replicas")
	}
}

func TestReconcile_ObserverStatus_NilWhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, updated))

	assert.Nil(t, updated.Status.ObserverReady, "observer status should be nil when observer is disabled")
}

// --- No-Master Recovery ---

func TestCheckAndRecoverNoMaster_SkipsSingleReplica(t *testing.T) {
	v := newTestValkey("test", "default")
	v.Spec.Replicas = 1
	r, _ := newTestReconciler(v)

	recovered, err := r.checkAndRecoverNoMaster(context.Background(), v)
	assert.NoError(t, err)
	assert.False(t, recovered, "should not attempt recovery on single-replica cluster")
}

func TestCheckAndRecoverNoMaster_SkipsDuringRollingUpdate(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	v.Annotations = map[string]string{
		annotationRollingUpdateState: "replacing-replicas",
	}

	mock := &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave"}, nil
		},
	}
	r, _ := newTestReconciler(v)
	r.InstanceChecker = mock

	recovered, err := r.checkAndRecoverNoMaster(context.Background(), v)
	assert.NoError(t, err)
	assert.False(t, recovered, "should not attempt recovery during rolling update")
}

func TestCheckAndRecoverNoMaster_NoRecoveryWhenMasterExists(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	mock := &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "test-0" {
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 2}, nil
			}
			return &valkeyclient.ReplicationInfo{Role: "slave"}, nil
		},
	}
	r, _ := newTestReconciler(v)
	r.InstanceChecker = mock

	recovered, err := r.checkAndRecoverNoMaster(context.Background(), v)
	assert.NoError(t, err)
	assert.False(t, recovered, "should not recover when a master exists")
}

func TestCheckAndRecoverNoMaster_NoRecoveryWhenPodUnreachable(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	mock := &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "test-2" {
				return nil, fmt.Errorf("connection refused")
			}
			return &valkeyclient.ReplicationInfo{Role: "slave"}, nil
		},
	}
	r, _ := newTestReconciler(v)
	r.InstanceChecker = mock

	recovered, err := r.checkAndRecoverNoMaster(context.Background(), v)
	assert.NoError(t, err)
	assert.False(t, recovered, "should not recover when a pod is unreachable")
}

func TestCheckAndRecoverNoMaster_RecoverWhenAllReplicas(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	mock := &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave"}, nil
		},
	}
	r, c := newTestReconciler(v)
	r.InstanceChecker = mock

	// First reconcile to create the resources.
	reconcileOnce(t, r, "test", "default")

	// Re-fetch to get the latest resource version.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v))

	// The recovery detects the no-master state and attempts REPLICAOF NO ONE.
	// In unit tests, the actual Valkey connection fails, returning an error.
	// This confirms the detection logic is correct — the REPLICAOF call is expected
	// to fail since there is no real Valkey instance.
	recovered, err := r.checkAndRecoverNoMaster(context.Background(), v)

	// Phase should be set to Error with recovery message (happens before REPLICAOF).
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, updated))
	assert.Equal(t, vkov1.ValkeyPhaseError, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "No master detected")

	// REPLICAOF NO ONE will fail in unit tests (no real pod), so err is expected.
	// The important assertion is that the function correctly detected the no-master
	// state and set the phase before attempting recovery.
	if err != nil {
		assert.Contains(t, err.Error(), "REPLICAOF NO ONE")
		assert.False(t, recovered)
	} else {
		assert.True(t, recovered)
	}
}
