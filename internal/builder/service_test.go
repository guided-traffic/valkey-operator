package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// --- BuildHeadlessService ---

func TestBuildHeadlessService(t *testing.T) {
	v := newTestValkey("my-valkey")

	svc := BuildHeadlessService(v)

	assert.Equal(t, "my-valkey-headless", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.True(t, svc.Spec.PublishNotReadyAddresses)
	assert.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)

	// Ports.
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, "valkey", svc.Spec.Ports[0].Name)
	assert.Equal(t, int32(ValkeyPort), svc.Spec.Ports[0].Port)

	// Selector labels.
	assert.Equal(t, "my-valkey", svc.Spec.Selector["app.kubernetes.io/instance"])
	assert.Equal(t, "vko.gtrfc.com", svc.Spec.Selector["app.kubernetes.io/managed-by"])
	assert.Equal(t, "valkey", svc.Spec.Selector["app.kubernetes.io/component"])

	// Labels on the service itself.
	assert.Equal(t, "valkey", svc.Labels["app.kubernetes.io/component"])
}

// --- BuildRWService ---

func TestBuildRWService(t *testing.T) {
	v := newTestValkey("my-valkey")

	svc := BuildRWService(v)

	assert.Equal(t, "my-valkey-rw", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.NotEqual(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)

	// Ports.
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(ValkeyPort), svc.Spec.Ports[0].Port)

	// Selector must include instanceRole=master.
	assert.Equal(t, "my-valkey", svc.Spec.Selector["app.kubernetes.io/instance"])
	assert.Equal(t, "master", svc.Spec.Selector["vko.gtrfc.com/instanceRole"])
}

func TestBuildRWService_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	svc := BuildRWService(v)

	assert.Equal(t, "production", svc.Namespace)
}

// --- BuildAllService ---

func TestBuildAllService(t *testing.T) {
	v := newTestValkey("my-valkey")

	svc := BuildAllService(v)

	assert.Equal(t, "my-valkey-all", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.NotEqual(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)

	// Ports.
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(ValkeyPort), svc.Spec.Ports[0].Port)

	// Selector should be all Valkey pods (no instanceRole filter).
	assert.Equal(t, "my-valkey", svc.Spec.Selector["app.kubernetes.io/instance"])
	assert.Equal(t, "valkey", svc.Spec.Selector["app.kubernetes.io/component"])
	_, hasRole := svc.Spec.Selector["vko.gtrfc.com/instanceRole"]
	assert.False(t, hasRole, "all-pods service should not filter by role")
}

// --- BuildReadOnlyService ---

func TestBuildReadOnlyService(t *testing.T) {
	v := newTestValkey("my-valkey")

	svc := BuildReadOnlyService(v)

	assert.Equal(t, "my-valkey-r", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)

	// Ports.
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(ValkeyPort), svc.Spec.Ports[0].Port)

	// Selector must include instanceRole=replica.
	assert.Equal(t, "my-valkey", svc.Spec.Selector["app.kubernetes.io/instance"])
	assert.Equal(t, "replica", svc.Spec.Selector["vko.gtrfc.com/instanceRole"])
}

// --- BuildSentinelHeadlessService ---

func TestBuildSentinelHeadlessService(t *testing.T) {
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-valkey",
			Namespace: "default",
		},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			Sentinel: &vkov1.SentinelSpec{
				Enabled:  true,
				Replicas: 3,
			},
		},
	}

	svc := BuildSentinelHeadlessService(v)

	assert.Equal(t, "my-valkey-sentinel-headless", svc.Name)
	assert.Equal(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.True(t, svc.Spec.PublishNotReadyAddresses)

	// Sentinel port.
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(26379), svc.Spec.Ports[0].Port)
	assert.Equal(t, "sentinel", svc.Spec.Ports[0].Name)

	// Selector should be for sentinel component.
	assert.Equal(t, "sentinel", svc.Spec.Selector["app.kubernetes.io/component"])

	// Labels on service itself.
	assert.Equal(t, "sentinel", svc.Labels["app.kubernetes.io/component"])
}

// --- AllServiceName ---

func TestAllServiceName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-all", AllServiceName(v))
}

// --- RWServiceName ---

func TestRWServiceName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-rw", RWServiceName(v))
}

// --- ReadOnlyServiceName ---

func TestReadOnlyServiceName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-r", ReadOnlyServiceName(v))
}
