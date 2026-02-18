package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// --- NetworkPolicyName ---

func TestNetworkPolicyName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey", NetworkPolicyName(v))
}

func TestNetworkPolicyName_WithPrefix(t *testing.T) {
	v := newTestValkey("my-valkey", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{
			Enabled:    true,
			NamePrefix: "my-prefix",
		}
	})
	assert.Equal(t, "my-prefix-my-valkey", NetworkPolicyName(v))
}

// --- SentinelNetworkPolicyName ---

func TestSentinelNetworkPolicyName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-sentinel", SentinelNetworkPolicyName(v))
}

func TestSentinelNetworkPolicyName_WithPrefix(t *testing.T) {
	v := newTestValkey("my-valkey", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{
			Enabled:    true,
			NamePrefix: "custom",
		}
	})
	assert.Equal(t, "custom-my-valkey-sentinel", SentinelNetworkPolicyName(v))
}

// --- BuildValkeyNetworkPolicy (Standalone) ---

func TestBuildValkeyNetworkPolicy_Standalone(t *testing.T) {
	v := newTestValkey("test")

	np := BuildValkeyNetworkPolicy(v)

	assert.Equal(t, "test", np.Name)
	assert.Equal(t, "default", np.Namespace)

	// Labels.
	assert.Equal(t, "valkey", np.Labels["app.kubernetes.io/component"])
	assert.Equal(t, "test", np.Labels["app.kubernetes.io/instance"])

	// Pod selector targets Valkey pods.
	assert.Equal(t, "test", np.Spec.PodSelector.MatchLabels["app.kubernetes.io/instance"])
	assert.Equal(t, "valkey", np.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"])

	// PolicyTypes.
	assert.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, np.Spec.PolicyTypes)

	// Ingress rules: only Valkey port, only from Valkey pods (no Sentinel).
	require.Len(t, np.Spec.Ingress, 1)
	require.Len(t, np.Spec.Ingress[0].Ports, 1)
	assert.Equal(t, intstr.FromInt32(ValkeyPort), *np.Spec.Ingress[0].Ports[0].Port)

	require.Len(t, np.Spec.Ingress[0].From, 1)
	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])
}

// --- BuildValkeyNetworkPolicy (HA with Sentinel) ---

func TestBuildValkeyNetworkPolicy_WithSentinel(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	np := BuildValkeyNetworkPolicy(v)

	// Ingress: Valkey port from Valkey + Sentinel.
	require.Len(t, np.Spec.Ingress, 1)
	require.Len(t, np.Spec.Ingress[0].From, 2)

	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, "sentinel", np.Spec.Ingress[0].From[1].PodSelector.MatchLabels["app.kubernetes.io/component"])
}

// --- BuildValkeyNetworkPolicy (with TLS) ---

func TestBuildValkeyNetworkPolicy_WithTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	np := BuildValkeyNetworkPolicy(v)

	// Should have 2 ingress rules: one for the plain port, one for the TLS port.
	require.Len(t, np.Spec.Ingress, 2)

	assert.Equal(t, intstr.FromInt32(ValkeyPort), *np.Spec.Ingress[0].Ports[0].Port)
	assert.Equal(t, intstr.FromInt32(int32(ValkeyPort+10000)), *np.Spec.Ingress[1].Ports[0].Port)
}

// --- BuildValkeyNetworkPolicy (HA + TLS) ---

func TestBuildValkeyNetworkPolicy_SentinelAndTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	np := BuildValkeyNetworkPolicy(v)

	// 2 ingress rules (plain + TLS), each with 2 peers (Valkey + Sentinel).
	require.Len(t, np.Spec.Ingress, 2)
	assert.Len(t, np.Spec.Ingress[0].From, 2)
	assert.Len(t, np.Spec.Ingress[1].From, 2)
}

// --- BuildValkeyNetworkPolicy (with NamePrefix) ---

func TestBuildValkeyNetworkPolicy_NamePrefix(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{
			Enabled:    true,
			NamePrefix: "my-prefix",
		}
	})

	np := BuildValkeyNetworkPolicy(v)
	assert.Equal(t, "my-prefix-test", np.Name)
}

// --- BuildValkeyNetworkPolicy protocol ---

func TestBuildValkeyNetworkPolicy_TCP(t *testing.T) {
	v := newTestValkey("test")
	np := BuildValkeyNetworkPolicy(v)

	require.Len(t, np.Spec.Ingress[0].Ports, 1)
	assert.Equal(t, corev1.ProtocolTCP, *np.Spec.Ingress[0].Ports[0].Protocol)
}

// --- BuildSentinelNetworkPolicy ---

func TestBuildSentinelNetworkPolicy(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	np := BuildSentinelNetworkPolicy(v)

	assert.Equal(t, "test-sentinel", np.Name)
	assert.Equal(t, "default", np.Namespace)

	// Labels.
	assert.Equal(t, "sentinel", np.Labels["app.kubernetes.io/component"])

	// Pod selector targets Sentinel pods.
	assert.Equal(t, "sentinel", np.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"])

	// PolicyTypes.
	assert.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, np.Spec.PolicyTypes)

	// Ingress: Sentinel port from Sentinel + Valkey.
	require.Len(t, np.Spec.Ingress, 1)
	require.Len(t, np.Spec.Ingress[0].Ports, 1)
	assert.Equal(t, intstr.FromInt32(SentinelPort), *np.Spec.Ingress[0].Ports[0].Port)

	require.Len(t, np.Spec.Ingress[0].From, 2)
	assert.Equal(t, "sentinel", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[1].PodSelector.MatchLabels["app.kubernetes.io/component"])
}

// --- BuildSentinelNetworkPolicy (with TLS) ---

func TestBuildSentinelNetworkPolicy_WithTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	np := BuildSentinelNetworkPolicy(v)

	// 2 ingress rules: Sentinel port + Sentinel TLS port.
	require.Len(t, np.Spec.Ingress, 2)
	assert.Equal(t, intstr.FromInt32(SentinelPort), *np.Spec.Ingress[0].Ports[0].Port)
	assert.Equal(t, intstr.FromInt32(int32(SentinelPort+10000)), *np.Spec.Ingress[1].Ports[0].Port)
}

// --- BuildSentinelNetworkPolicy (with NamePrefix) ---

func TestBuildSentinelNetworkPolicy_NamePrefix(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{
			Enabled:    true,
			NamePrefix: "custom",
		}
	})

	np := BuildSentinelNetworkPolicy(v)
	assert.Equal(t, "custom-test-sentinel", np.Name)
}

// --- NetworkPolicyHasChanged ---

func TestNetworkPolicyHasChanged_Identical(t *testing.T) {
	v := newTestValkey("test")
	a := BuildValkeyNetworkPolicy(v)
	b := BuildValkeyNetworkPolicy(v)

	assert.False(t, NetworkPolicyHasChanged(a, b))
}

func TestNetworkPolicyHasChanged_DifferentIngressRuleCount(t *testing.T) {
	v1 := newTestValkey("test")
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	a := BuildValkeyNetworkPolicy(v1)
	b := BuildValkeyNetworkPolicy(v2)

	assert.True(t, NetworkPolicyHasChanged(a, b))
}

func TestNetworkPolicyHasChanged_DifferentPeerCount(t *testing.T) {
	v1 := newTestValkey("test")
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	a := BuildValkeyNetworkPolicy(v1)
	b := BuildValkeyNetworkPolicy(v2)

	assert.True(t, NetworkPolicyHasChanged(a, b))
}

// --- Namespace propagation ---

func TestBuildValkeyNetworkPolicy_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	np := BuildValkeyNetworkPolicy(v)
	assert.Equal(t, "production", np.Namespace)
}

func TestBuildSentinelNetworkPolicy_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	np := BuildSentinelNetworkPolicy(v)
	assert.Equal(t, "production", np.Namespace)
}
