package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = vkov1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	return s
}

func newTestReconciler(objs ...client.Object) (*ValkeyReconciler, client.Client) {
	s := testScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&vkov1.Valkey{}).
		Build()

	return &ValkeyReconciler{
		Client: fakeClient,
		Scheme: s,
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
