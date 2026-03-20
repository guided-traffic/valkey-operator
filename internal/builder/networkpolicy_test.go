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

	np := BuildValkeyNetworkPolicy(v, "")

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

	// Ingress rules: Valkey port (from Valkey pods) + sidecar health port (open to all).
	require.Len(t, np.Spec.Ingress, 2)

	// Rule 0: Valkey port from Valkey pods only.
	require.Len(t, np.Spec.Ingress[0].Ports, 1)
	assert.Equal(t, intstr.FromInt32(ValkeyPort), *np.Spec.Ingress[0].Ports[0].Port)
	require.Len(t, np.Spec.Ingress[0].From, 1)
	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])

	// Rule 1: sidecar health port open to all (no From restriction) for kubelet probes.
	require.Len(t, np.Spec.Ingress[1].Ports, 1)
	assert.Equal(t, intstr.FromInt32(SidecarHealthPort), *np.Spec.Ingress[1].Ports[0].Port)
	assert.Empty(t, np.Spec.Ingress[1].From, "health port must be open to all sources")
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

	np := BuildValkeyNetworkPolicy(v, "")

	// Ingress: Valkey port from Valkey+Sentinel + sidecar health port open to all.
	require.Len(t, np.Spec.Ingress, 2)
	require.Len(t, np.Spec.Ingress[0].From, 2)

	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, "sentinel", np.Spec.Ingress[0].From[1].PodSelector.MatchLabels["app.kubernetes.io/component"])

	// Health port rule (last rule, no From restriction).
	assert.Equal(t, intstr.FromInt32(SidecarHealthPort), *np.Spec.Ingress[1].Ports[0].Port)
	assert.Empty(t, np.Spec.Ingress[1].From)
}

// --- BuildValkeyNetworkPolicy (with TLS) ---

func TestBuildValkeyNetworkPolicy_WithTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	np := BuildValkeyNetworkPolicy(v, "")

	// Should have 3 ingress rules: plain port, TLS port, and sidecar health port.
	require.Len(t, np.Spec.Ingress, 3)

	assert.Equal(t, intstr.FromInt32(ValkeyPort), *np.Spec.Ingress[0].Ports[0].Port)
	assert.Equal(t, intstr.FromInt32(int32(ValkeyPort+10000)), *np.Spec.Ingress[1].Ports[0].Port)
	assert.Equal(t, intstr.FromInt32(SidecarHealthPort), *np.Spec.Ingress[2].Ports[0].Port)
	assert.Empty(t, np.Spec.Ingress[2].From, "health port must be open to all sources")
}

// --- BuildValkeyNetworkPolicy (HA + TLS) ---

func TestBuildValkeyNetworkPolicy_SentinelAndTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	np := BuildValkeyNetworkPolicy(v, "")

	// 3 ingress rules: plain Valkey port, TLS Valkey port, and sidecar health port.
	// Plain and TLS rules each have 2 peers (Valkey + Sentinel); health port has no From restriction.
	require.Len(t, np.Spec.Ingress, 3)
	assert.Len(t, np.Spec.Ingress[0].From, 2)
	assert.Len(t, np.Spec.Ingress[1].From, 2)
	assert.Equal(t, intstr.FromInt32(SidecarHealthPort), *np.Spec.Ingress[2].Ports[0].Port)
	assert.Empty(t, np.Spec.Ingress[2].From, "health port must be open to all sources")
}

// --- BuildValkeyNetworkPolicy (with NamePrefix) ---

func TestBuildValkeyNetworkPolicy_NamePrefix(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{
			Enabled:    true,
			NamePrefix: "my-prefix",
		}
	})

	np := BuildValkeyNetworkPolicy(v, "")
	assert.Equal(t, "my-prefix-test", np.Name)
}

// --- BuildValkeyNetworkPolicy protocol ---

func TestBuildValkeyNetworkPolicy_TCP(t *testing.T) {
	v := newTestValkey("test")
	np := BuildValkeyNetworkPolicy(v, "")

	require.Len(t, np.Spec.Ingress[0].Ports, 1)
	assert.Equal(t, corev1.ProtocolTCP, *np.Spec.Ingress[0].Ports[0].Protocol)
}

// --- BuildValkeyNetworkPolicy: sidecar health port ---

// TestBuildValkeyNetworkPolicy_SidecarHealthPort verifies that the last ingress
// rule always allows traffic on SidecarHealthPort from all sources (no From
// restriction) so that kubelet readiness/liveness probes are never blocked.
func TestBuildValkeyNetworkPolicy_SidecarHealthPort(t *testing.T) {
	testCases := []struct {
		name    string
		mutator func(v *vkov1.Valkey)
	}{
		{"standalone", func(_ *vkov1.Valkey) {}},
		{"with-sentinel", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		}},
		{"with-tls", func(v *vkov1.Valkey) {
			v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		}},
		{"sentinel-and-tls", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
			v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("test", tc.mutator)
			np := BuildValkeyNetworkPolicy(v, "")

			// The last ingress rule is always the health port.
			last := np.Spec.Ingress[len(np.Spec.Ingress)-1]
			require.Len(t, last.Ports, 1)
			assert.Equal(t, intstr.FromInt32(SidecarHealthPort), *last.Ports[0].Port)
			assert.Equal(t, corev1.ProtocolTCP, *last.Ports[0].Protocol)
			assert.Empty(t, last.From, "sidecar health port must be reachable from all sources (kubelet)")
		})
	}
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

	np := BuildSentinelNetworkPolicy(v, "")

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

	np := BuildSentinelNetworkPolicy(v, "")

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

	np := BuildSentinelNetworkPolicy(v, "")
	assert.Equal(t, "custom-test-sentinel", np.Name)
}

// --- NetworkPolicyHasChanged ---

func TestNetworkPolicyHasChanged_Identical(t *testing.T) {
	v := newTestValkey("test")
	a := BuildValkeyNetworkPolicy(v, "")
	b := BuildValkeyNetworkPolicy(v, "")

	assert.False(t, NetworkPolicyHasChanged(a, b))
}

func TestNetworkPolicyHasChanged_DifferentIngressRuleCount(t *testing.T) {
	v1 := newTestValkey("test")
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	a := BuildValkeyNetworkPolicy(v1, "")
	b := BuildValkeyNetworkPolicy(v2, "")

	assert.True(t, NetworkPolicyHasChanged(a, b))
}

func TestNetworkPolicyHasChanged_DifferentPeerCount(t *testing.T) {
	v1 := newTestValkey("test")
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	a := BuildValkeyNetworkPolicy(v1, "")
	b := BuildValkeyNetworkPolicy(v2, "")

	assert.True(t, NetworkPolicyHasChanged(a, b))
}

// --- Namespace propagation ---

func TestBuildValkeyNetworkPolicy_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	np := BuildValkeyNetworkPolicy(v, "")
	assert.Equal(t, "production", np.Namespace)
}

func TestBuildSentinelNetworkPolicy_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	np := BuildSentinelNetworkPolicy(v, "")
	assert.Equal(t, "production", np.Namespace)
}

// --- OperatorNamespace ingress peer ---

// TestBuildValkeyNetworkPolicy_OperatorNamespace verifies that when an operator
// namespace is provided, a NamespaceSelector-based ingress peer is appended so
// the operator pod can connect to Valkey for health checks.
func TestBuildValkeyNetworkPolicy_OperatorNamespace(t *testing.T) {
	v := newTestValkey("test")
	np := BuildValkeyNetworkPolicy(v, "database-operators")

	// Valkey port rule should now have 2 peers: Valkey pods + operator namespace.
	require.Len(t, np.Spec.Ingress[0].From, 2)

	// Last peer on the Valkey port rule is the operator namespace selector.
	opPeer := np.Spec.Ingress[0].From[1]
	assert.Nil(t, opPeer.PodSelector)
	require.NotNil(t, opPeer.NamespaceSelector)
	assert.Equal(t, "database-operators", opPeer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
}

func TestBuildValkeyNetworkPolicy_OperatorNamespace_WithSentinelAndTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	np := BuildValkeyNetworkPolicy(v, "ops-ns")

	// 3 rules: plain port, TLS port, health port.
	require.Len(t, np.Spec.Ingress, 3)

	// Plain port rule: Valkey + Sentinel + operator namespace = 3 peers.
	require.Len(t, np.Spec.Ingress[0].From, 3)
	// TLS port rule: same 3 peers.
	require.Len(t, np.Spec.Ingress[1].From, 3)

	// Operator namespace peer is last on each port rule.
	for _, ruleIdx := range []int{0, 1} {
		opPeer := np.Spec.Ingress[ruleIdx].From[2]
		assert.Nil(t, opPeer.PodSelector)
		require.NotNil(t, opPeer.NamespaceSelector)
		assert.Equal(t, "ops-ns", opPeer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
}

// TestBuildValkeyNetworkPolicy_NoOperatorNamespace verifies that when the
// operator namespace is empty, no NamespaceSelector peer is added.
func TestBuildValkeyNetworkPolicy_NoOperatorNamespace(t *testing.T) {
	v := newTestValkey("test")
	np := BuildValkeyNetworkPolicy(v, "")

	require.Len(t, np.Spec.Ingress[0].From, 1)
	assert.NotNil(t, np.Spec.Ingress[0].From[0].PodSelector)
	assert.Nil(t, np.Spec.Ingress[0].From[0].NamespaceSelector)
}

func TestBuildSentinelNetworkPolicy_OperatorNamespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	np := BuildSentinelNetworkPolicy(v, "database-operators")

	// Sentinel port rule: Sentinel + Valkey + operator namespace = 3 peers.
	require.Len(t, np.Spec.Ingress[0].From, 3)

	opPeer := np.Spec.Ingress[0].From[2]
	assert.Nil(t, opPeer.PodSelector)
	require.NotNil(t, opPeer.NamespaceSelector)
	assert.Equal(t, "database-operators", opPeer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
}

// --- Observer NetworkPolicy Tests ---

func TestObserverNetworkPolicyName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-observer", ObserverNetworkPolicyName(v))
}

func TestObserverNetworkPolicyName_WithPrefix(t *testing.T) {
	v := newTestValkey("my-valkey", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{
			Enabled:    true,
			NamePrefix: "custom",
		}
	})
	assert.Equal(t, "custom-my-valkey-observer", ObserverNetworkPolicyName(v))
}

func TestBuildObserverNetworkPolicy(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	np := BuildObserverNetworkPolicy(v)

	assert.Equal(t, "test-observer", np.Name)
	assert.Equal(t, "default", np.Namespace)

	// Labels.
	assert.Equal(t, ComponentObserver, np.Labels["app.kubernetes.io/component"])
	assert.Equal(t, "test", np.Labels["app.kubernetes.io/instance"])

	// Pod selector targets observer pods.
	assert.Equal(t, ComponentObserver, np.Spec.PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, "test", np.Spec.PodSelector.MatchLabels["vko.gtrfc.com/cluster"])

	// PolicyTypes.
	assert.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, np.Spec.PolicyTypes)

	// One ingress rule: health port open to all.
	require.Len(t, np.Spec.Ingress, 1)
	require.Len(t, np.Spec.Ingress[0].Ports, 1)
	assert.Equal(t, intstr.FromInt32(ObserverHealthPort), *np.Spec.Ingress[0].Ports[0].Port)
	assert.Equal(t, corev1.ProtocolTCP, *np.Spec.Ingress[0].Ports[0].Protocol)
	assert.Empty(t, np.Spec.Ingress[0].From, "health port must be open to all sources for kubelet probes")
}

func TestBuildValkeyNetworkPolicy_WithObserver(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	np := BuildValkeyNetworkPolicy(v, "")

	// Valkey port rule should have 2 peers: Valkey pods + observer pods.
	require.Len(t, np.Spec.Ingress[0].From, 2)
	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, ComponentObserver, np.Spec.Ingress[0].From[1].PodSelector.MatchLabels["app.kubernetes.io/component"])
}

func TestBuildSentinelNetworkPolicy_WithObserver(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	np := BuildSentinelNetworkPolicy(v, "")

	// Sentinel port rule: Sentinel + Valkey + Observer = 3 peers.
	require.Len(t, np.Spec.Ingress[0].From, 3)
	assert.Equal(t, "sentinel", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[1].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, ComponentObserver, np.Spec.Ingress[0].From[2].PodSelector.MatchLabels["app.kubernetes.io/component"])
}

// TestNetworkPolicyHasChanged_OperatorNamespaceDiffers verifies that adding or
// removing the operator namespace peer is detected as a change.
func TestNetworkPolicyHasChanged_OperatorNamespaceDiffers(t *testing.T) {
	v := newTestValkey("test")
	withNS := BuildValkeyNetworkPolicy(v, "database-operators")
	withoutNS := BuildValkeyNetworkPolicy(v, "")

	assert.True(t, NetworkPolicyHasChanged(withNS, withoutNS))
	assert.True(t, NetworkPolicyHasChanged(withoutNS, withNS))
}
