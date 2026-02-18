package builder

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// NetworkPolicyName returns the name for the Valkey NetworkPolicy.
func NetworkPolicyName(v *vkov1.Valkey) string {
	prefix := networkPolicyPrefix(v)
	return fmt.Sprintf("%s%s", prefix, v.Name)
}

// SentinelNetworkPolicyName returns the name for the Sentinel NetworkPolicy.
func SentinelNetworkPolicyName(v *vkov1.Valkey) string {
	prefix := networkPolicyPrefix(v)
	return fmt.Sprintf("%s%s-sentinel", prefix, v.Name)
}

// networkPolicyPrefix returns the name prefix for NetworkPolicies, including a trailing dash if set.
func networkPolicyPrefix(v *vkov1.Valkey) string {
	if v.Spec.NetworkPolicy != nil && v.Spec.NetworkPolicy.NamePrefix != "" {
		return v.Spec.NetworkPolicy.NamePrefix + "-"
	}
	return ""
}

// BuildValkeyNetworkPolicy builds the NetworkPolicy that allows Valkey↔Valkey
// and Sentinel→Valkey traffic within the cluster.
// It restricts ingress to the Valkey port from other Valkey pods and Sentinel pods.
func BuildValkeyNetworkPolicy(v *vkov1.Valkey) *networkingv1.NetworkPolicy {
	labels := common.BaseLabels(v, common.ComponentValkey)
	valkeySelector := common.SelectorLabels(v, common.ComponentValkey)

	valkeyPort := intstr.FromInt32(ValkeyPort)
	tcpProtocol := corev1.ProtocolTCP

	// Ingress peers: allow from Valkey pods (replication traffic).
	ingressPeers := []networkingv1.NetworkPolicyPeer{
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: common.SelectorLabels(v, common.ComponentValkey),
			},
		},
	}

	// If Sentinel is enabled, also allow ingress from Sentinel pods.
	if v.IsSentinelEnabled() {
		ingressPeers = append(ingressPeers, networkingv1.NetworkPolicyPeer{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: common.SelectorLabels(v, common.ComponentSentinel),
			},
		})
	}

	ingressRules := []networkingv1.NetworkPolicyIngressRule{
		{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: &tcpProtocol,
					Port:     &valkeyPort,
				},
			},
			From: ingressPeers,
		},
	}

	// If TLS is enabled, the TLS port is ValkeyPort+10000; allow that as well.
	if v.IsTLSEnabled() {
		tlsPort := intstr.FromInt32(int32(ValkeyPort + 10000))
		ingressRules = append(ingressRules, networkingv1.NetworkPolicyIngressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: &tcpProtocol,
					Port:     &tlsPort,
				},
			},
			From: ingressPeers,
		})
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkPolicyName(v),
			Namespace: v.Namespace,
			Labels:    labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: valkeySelector,
			},
			Ingress:     ingressRules,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
}

// BuildSentinelNetworkPolicy builds the NetworkPolicy that allows Valkey→Sentinel
// and Sentinel↔Sentinel traffic.
// It restricts ingress to the Sentinel port from Valkey and Sentinel pods.
func BuildSentinelNetworkPolicy(v *vkov1.Valkey) *networkingv1.NetworkPolicy {
	labels := common.BaseLabels(v, common.ComponentSentinel)
	sentinelSelector := common.SelectorLabels(v, common.ComponentSentinel)

	sentinelPort := intstr.FromInt32(SentinelPort)
	tcpProtocol := corev1.ProtocolTCP

	ingressPeers := []networkingv1.NetworkPolicyPeer{
		// Allow from Sentinel pods (inter-sentinel communication).
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: common.SelectorLabels(v, common.ComponentSentinel),
			},
		},
		// Allow from Valkey pods (Valkey querying Sentinel).
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: common.SelectorLabels(v, common.ComponentValkey),
			},
		},
	}

	ingressRules := []networkingv1.NetworkPolicyIngressRule{
		{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: &tcpProtocol,
					Port:     &sentinelPort,
				},
			},
			From: ingressPeers,
		},
	}

	// If TLS is enabled, the Sentinel TLS port is SentinelPort+10000.
	if v.IsTLSEnabled() {
		sentinelTLSPort := intstr.FromInt32(int32(SentinelPort + 10000))
		ingressRules = append(ingressRules, networkingv1.NetworkPolicyIngressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: &tcpProtocol,
					Port:     &sentinelTLSPort,
				},
			},
			From: ingressPeers,
		})
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SentinelNetworkPolicyName(v),
			Namespace: v.Namespace,
			Labels:    labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: sentinelSelector,
			},
			Ingress:     ingressRules,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
}

// NetworkPolicyHasChanged returns true if the desired NetworkPolicy differs from the current one.
func NetworkPolicyHasChanged(desired, current *networkingv1.NetworkPolicy) bool {
	if desired.Spec.PodSelector.String() != current.Spec.PodSelector.String() {
		return true
	}

	if len(desired.Spec.Ingress) != len(current.Spec.Ingress) {
		return true
	}

	for i := range desired.Spec.Ingress {
		if len(desired.Spec.Ingress[i].Ports) != len(current.Spec.Ingress[i].Ports) {
			return true
		}
		if len(desired.Spec.Ingress[i].From) != len(current.Spec.Ingress[i].From) {
			return true
		}

		for j := range desired.Spec.Ingress[i].Ports {
			dp := desired.Spec.Ingress[i].Ports[j]
			cp := current.Spec.Ingress[i].Ports[j]
			if dp.Port.String() != cp.Port.String() {
				return true
			}
		}

		for j := range desired.Spec.Ingress[i].From {
			df := desired.Spec.Ingress[i].From[j]
			cf := current.Spec.Ingress[i].From[j]
			if df.PodSelector.String() != cf.PodSelector.String() {
				return true
			}
		}
	}

	return false
}
