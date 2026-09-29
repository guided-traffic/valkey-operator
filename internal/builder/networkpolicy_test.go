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

	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})

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

	// One ingress rule: the Valkey port from Valkey pods only.
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

	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})

	// Ingress: the Valkey port from Valkey and Sentinel pods.
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

	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})

	// Two ingress rules: the plain port and the TLS port.
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

	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})

	// Two ingress rules, plain and TLS Valkey port, each with 2 peers (Valkey + Sentinel).
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

	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})
	assert.Equal(t, "my-prefix-test", np.Name)
}

// --- BuildValkeyNetworkPolicy protocol ---

func TestBuildValkeyNetworkPolicy_TCP(t *testing.T) {
	v := newTestValkey("test")
	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})

	require.Len(t, np.Spec.Ingress[0].Ports, 1)
	assert.Equal(t, corev1.ProtocolTCP, *np.Spec.Ingress[0].Ports[0].Protocol)
}

// --- Only the deployed components are admitted (ADR 0039 D1, D2) ---

// testOperator is an operator peer as the chart passes it.
var testOperator = OperatorPeer{
	Namespace: "database-operators",
	PodLabels: map[string]string{
		"app.kubernetes.io/name":      "valkey-operator",
		"app.kubernetes.io/instance":  "vko",
		"app.kubernetes.io/component": "operator",
	},
}

// allFeatures turns on every component and port a policy could be widened for.
func allFeatures(v *vkov1.Valkey) {
	v.Spec.Replicas = 3
	v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
}

// assertOnlyComponentRules fails on a rule without a From (every source), on a
// peer that is a namespace alone, on an ipBlock, and on a port outside allowed.
func assertOnlyComponentRules(t *testing.T, np *networkingv1.NetworkPolicy, allowed ...int32) {
	t.Helper()
	for i, rule := range np.Spec.Ingress {
		assert.NotEmpty(t, rule.From, "rule %d admits every source", i)
		for _, peer := range rule.From {
			assert.Nil(t, peer.IPBlock, "rule %d carries an ipBlock", i)
			assert.NotNil(t, peer.PodSelector, "rule %d admits a whole namespace", i)
		}
		for _, p := range rule.Ports {
			assert.Contains(t, allowed, p.Port.IntVal, "rule %d opens port %s", i, p.Port.String())
		}
	}
}

// The data policy opens the data ports and nothing else: no rule for the sidecar
// health port (kubelet comes from the node) and none for the exporter port (a
// scraper is the administrator's to admit), in every topology.
func TestBuildValkeyNetworkPolicy_NoRuleOutsideTheDataPorts(t *testing.T) {
	testCases := []struct {
		name    string
		mutator func(v *vkov1.Valkey)
	}{
		{"standalone", func(_ *vkov1.Valkey) {}},
		{"with-metrics", func(v *vkov1.Valkey) {
			v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true, Port: 9999}
		}},
		{"everything", allFeatures},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			np := BuildValkeyNetworkPolicy(newTestValkey("test", tc.mutator), testOperator)
			assertOnlyComponentRules(t, np, ValkeyPort, ValkeyPort+10000)
		})
	}
}

func TestBuildSentinelNetworkPolicy_NoRuleOutsideTheSentinelPorts(t *testing.T) {
	np := BuildSentinelNetworkPolicy(newTestValkey("test", allFeatures), testOperator)
	assertOnlyComponentRules(t, np, SentinelPort, SentinelPort+10000)
}

// Enabling metrics does not change the data policy at all: nothing this
// repository deploys reads the exporter port.
func TestBuildValkeyNetworkPolicy_MetricsAddNoRule(t *testing.T) {
	withMetrics := BuildValkeyNetworkPolicy(newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	}), testOperator)
	without := BuildValkeyNetworkPolicy(newTestValkey("test"), testOperator)

	assert.Equal(t, without.Spec.Ingress, withMetrics.Spec.Ingress)
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

	np := BuildSentinelNetworkPolicy(v, OperatorPeer{})

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

	np := BuildSentinelNetworkPolicy(v, OperatorPeer{})

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

	np := BuildSentinelNetworkPolicy(v, OperatorPeer{})
	assert.Equal(t, "custom-test-sentinel", np.Name)
}

// --- NetworkPolicyHasChanged ---

func TestNetworkPolicyHasChanged_Identical(t *testing.T) {
	v := newTestValkey("test")
	a := BuildValkeyNetworkPolicy(v, OperatorPeer{})
	b := BuildValkeyNetworkPolicy(v, OperatorPeer{})

	assert.False(t, NetworkPolicyHasChanged(a, b))
}

func TestNetworkPolicyHasChanged_DifferentIngressRuleCount(t *testing.T) {
	v1 := newTestValkey("test")
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	a := BuildValkeyNetworkPolicy(v1, OperatorPeer{})
	b := BuildValkeyNetworkPolicy(v2, OperatorPeer{})

	assert.True(t, NetworkPolicyHasChanged(a, b))
}

func TestNetworkPolicyHasChanged_DifferentPeerCount(t *testing.T) {
	v1 := newTestValkey("test")
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	a := BuildValkeyNetworkPolicy(v1, OperatorPeer{})
	b := BuildValkeyNetworkPolicy(v2, OperatorPeer{})

	assert.True(t, NetworkPolicyHasChanged(a, b))
}

// --- Namespace propagation ---

func TestBuildValkeyNetworkPolicy_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})
	assert.Equal(t, "production", np.Namespace)
}

func TestBuildSentinelNetworkPolicy_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	np := BuildSentinelNetworkPolicy(v, OperatorPeer{})
	assert.Equal(t, "production", np.Namespace)
}

// --- The operator peer (ADR 0039 D2) ---

// assertOperatorPeer checks that peer admits the operator pod alone: its
// namespace AND its labels, in one peer.
func assertOperatorPeer(t *testing.T, peer networkingv1.NetworkPolicyPeer) {
	t.Helper()
	require.NotNil(t, peer.NamespaceSelector)
	assert.Equal(t, map[string]string{"kubernetes.io/metadata.name": "database-operators"},
		peer.NamespaceSelector.MatchLabels)
	require.NotNil(t, peer.PodSelector, "a namespace alone admits every pod in it")
	assert.Equal(t, testOperator.PodLabels, peer.PodSelector.MatchLabels)
}

// TestBuildValkeyNetworkPolicy_OperatorPeer verifies that the operator is
// admitted as its pod, never as its namespace, on both data ports.
func TestBuildValkeyNetworkPolicy_OperatorPeer(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	np := BuildValkeyNetworkPolicy(v, testOperator)

	require.Len(t, np.Spec.Ingress, 2)
	for _, rule := range np.Spec.Ingress {
		// Valkey + Sentinel + operator; the operator is last.
		require.Len(t, rule.From, 3)
		assertOperatorPeer(t, rule.From[2])
	}
}

func TestBuildSentinelNetworkPolicy_OperatorPeer(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	np := BuildSentinelNetworkPolicy(v, testOperator)

	// Sentinel port rule: Sentinel + Valkey + operator = 3 peers.
	require.Len(t, np.Spec.Ingress[0].From, 3)
	assertOperatorPeer(t, np.Spec.Ingress[0].From[2])
}

// Half an identity is no identity: a namespace alone would admit every pod in
// it, labels alone would select pods in the Valkey namespace. Either way no
// operator peer is written.
func TestBuildNetworkPolicy_IncompleteOperatorPeerAdmitsNoOperator(t *testing.T) {
	for name, peer := range map[string]OperatorPeer{
		"nothing":        {},
		"namespace only": {Namespace: "database-operators"},
		"labels only":    {PodLabels: testOperator.PodLabels},
	} {
		t.Run(name, func(t *testing.T) {
			v := newTestValkey("test", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
			})
			valkey := BuildValkeyNetworkPolicy(v, peer)
			sentinel := BuildSentinelNetworkPolicy(v, peer)

			assert.Len(t, valkey.Spec.Ingress[0].From, 2, "Valkey + Sentinel, no operator")
			assert.Len(t, sentinel.Spec.Ingress[0].From, 2, "Sentinel + Valkey, no operator")
			for _, p := range append(valkey.Spec.Ingress[0].From, sentinel.Spec.Ingress[0].From...) {
				assert.Nil(t, p.NamespaceSelector)
			}
		})
	}
}

// The peer's label map is the policy's own: the client decodes the server's
// answer into the object it wrote, and the reconciler's map is shared by every
// concurrent pass.
func TestBuildValkeyNetworkPolicy_OperatorPeerLabelsAreCopied(t *testing.T) {
	np := BuildValkeyNetworkPolicy(newTestValkey("test"), testOperator)
	np.Spec.Ingress[0].From[1].PodSelector.MatchLabels["written"] = "by the decoder"

	assert.NotContains(t, testOperator.PodLabels, "written")
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

	// No ingress rule: nothing deployed connects to the observer, kubelet comes
	// from the node. Nil rather than empty, or the drift check would see the API
	// server's answer (the field omitted) as a difference on every pass.
	assert.Nil(t, np.Spec.Ingress)
}

func TestBuildValkeyNetworkPolicy_WithObserver(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	np := BuildValkeyNetworkPolicy(v, OperatorPeer{})

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

	np := BuildSentinelNetworkPolicy(v, OperatorPeer{})

	// Sentinel port rule: Sentinel + Valkey + Observer = 3 peers.
	require.Len(t, np.Spec.Ingress[0].From, 3)
	assert.Equal(t, "sentinel", np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, "valkey", np.Spec.Ingress[0].From[1].PodSelector.MatchLabels["app.kubernetes.io/component"])
	assert.Equal(t, ComponentObserver, np.Spec.Ingress[0].From[2].PodSelector.MatchLabels["app.kubernetes.io/component"])
}

// TestNetworkPolicyHasChanged_OperatorPeerDiffers verifies that adding or
// removing the operator peer is detected as a change.
func TestNetworkPolicyHasChanged_OperatorPeerDiffers(t *testing.T) {
	v := newTestValkey("test")
	withPeer := BuildValkeyNetworkPolicy(v, testOperator)
	withoutPeer := BuildValkeyNetworkPolicy(v, OperatorPeer{})

	assert.True(t, NetworkPolicyHasChanged(withPeer, withoutPeer))
	assert.True(t, NetworkPolicyHasChanged(withoutPeer, withPeer))
}

// NetworkPolicyHasChanged decides whether the live policy is rewritten. The pod
// selector is the half that says WHICH pods are firewalled: a policy that kept an
// outdated selector would leave the real pods unprotected while looking correct.
func TestNetworkPolicyHasChanged_DifferentPodSelector(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildValkeyNetworkPolicy(v, OperatorPeer{})
	current := desired.DeepCopy()
	current.Spec.PodSelector.MatchLabels["app.kubernetes.io/instance"] = "some-other-cluster"

	assert.True(t, NetworkPolicyHasChanged(desired, current))
}
