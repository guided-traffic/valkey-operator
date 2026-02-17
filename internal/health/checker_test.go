package health

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

func newTestValkey(name, ns string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
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
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// --- PodAddressForComponent ---

func TestPodAddressForComponent_Valkey(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := PodAddressForComponent(v, "test-0", common.ComponentValkey, builder.ValkeyPort)

	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local:6379", addr)
}

func TestPodAddressForComponent_Sentinel(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := PodAddressForComponent(v, "test-sentinel-0", common.ComponentSentinel, builder.SentinelPort)

	assert.Equal(t, "test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379", addr)
}

func TestPodAddressForComponent_CustomNamespace(t *testing.T) {
	v := newTestValkey("test", "production")
	addr := PodAddressForComponent(v, "test-1", common.ComponentValkey, builder.ValkeyPort)

	assert.Equal(t, "test-1.test-headless.production.svc.cluster.local:6379", addr)
}

// --- NewChecker ---

func TestNewChecker(t *testing.T) {
	c := NewChecker(nil)
	assert.NotNil(t, c)
}

// --- ClusterState ---

func TestClusterState_Defaults(t *testing.T) {
	state := &ClusterState{}
	assert.Equal(t, "", state.MasterPod)
	assert.Equal(t, int32(0), state.ReadyReplicas)
	assert.False(t, state.AllSynced)
	assert.False(t, state.SentinelMonitoring)
	assert.Nil(t, state.Error)
}
