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

// --- BuildClientService ---

func TestBuildClientService(t *testing.T) {
	v := newTestValkey("my-valkey")

	svc := BuildClientService(v)

	assert.Equal(t, "my-valkey", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.NotEqual(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
	assert.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)

	// Ports.
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(ValkeyPort), svc.Spec.Ports[0].Port)

	// Selector.
	assert.Equal(t, "my-valkey", svc.Spec.Selector["app.kubernetes.io/instance"])
}

func TestBuildClientService_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	svc := BuildClientService(v)

	assert.Equal(t, "production", svc.Namespace)
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

// --- ClientServiceName ---

func TestClientServiceName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey", ClientServiceName(v))
}

// --- ReadServiceName ---

func TestReadServiceName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-read", ReadServiceName(v))
}
