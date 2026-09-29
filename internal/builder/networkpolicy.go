package builder

import (
	"fmt"
	"reflect"

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

// OperatorPeer identifies the operator pod for the generated NetworkPolicies:
// the namespace it runs in and labels that select it alone, not the chart's
// pre-upgrade hook and not any other pod of that namespace (ADR 0039 D2).
type OperatorPeer struct {
	Namespace string
	PodLabels map[string]string
}

// networkPolicyPeer returns the peer that admits the operator pod, and false
// when either half is unknown: a namespace alone would admit every pod in it,
// labels alone would select pods in the Valkey resource's namespace. Without
// both the policies admit no operator at all (ADR 0039, Residual risks).
func (o OperatorPeer) networkPolicyPeer() (networkingv1.NetworkPolicyPeer, bool) {
	if o.Namespace == "" || len(o.PodLabels) == 0 {
		return networkingv1.NetworkPolicyPeer{}, false
	}
	// A copy: the client decodes the server's answer into the object it wrote, and
	// the reconciler's one map would be written by every concurrent pass (ADR 0019).
	podLabels := make(map[string]string, len(o.PodLabels))
	for k, val := range o.PodLabels {
		podLabels[k] = val
	}
	return networkingv1.NetworkPolicyPeer{
		// Both selectors in one peer: the pod must match both.
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"kubernetes.io/metadata.name": o.Namespace},
		},
		PodSelector: &metav1.LabelSelector{MatchLabels: podLabels},
	}, true
}

// BuildValkeyNetworkPolicy builds the NetworkPolicy of the data pods. It admits
// the data port (and the TLS port) from the components this repository deploys
// and nothing else (ADR 0039 D1, D2): the other data pods, the Sentinel and
// observer pods when enabled, and the operator pod. The sidecar health port and
// the exporter port get no rule: kubelet's probes come from the node, which the
// API admits anyway, and a scraper is the administrator's to admit (D3).
func BuildValkeyNetworkPolicy(v *vkov1.Valkey, operator OperatorPeer) *networkingv1.NetworkPolicy {
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

	// If observer is enabled, also allow ingress from observer pods.
	if v.IsObserverEnabled() {
		ingressPeers = append(ingressPeers, networkingv1.NetworkPolicyPeer{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: ObserverSelectorLabels(v),
			},
		})
	}

	// The operator reaches the data pods for its health checks (INFO replication).
	if peer, ok := operator.networkPolicyPeer(); ok {
		ingressPeers = append(ingressPeers, peer)
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

// BuildSentinelNetworkPolicy builds the NetworkPolicy of the Sentinel pods. It
// admits the Sentinel port (and the TLS port) from the other Sentinel pods, the
// data pods, the observer pods when enabled, and the operator pod (ADR 0039 D2).
func BuildSentinelNetworkPolicy(v *vkov1.Valkey, operator OperatorPeer) *networkingv1.NetworkPolicy {
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

	// If observer is enabled, also allow ingress from observer pods.
	if v.IsObserverEnabled() {
		ingressPeers = append(ingressPeers, networkingv1.NetworkPolicyPeer{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: ObserverSelectorLabels(v),
			},
		})
	}

	// The operator reaches the Sentinel pods for its health checks (SENTINEL MASTER).
	if peer, ok := operator.networkPolicyPeer(); ok {
		ingressPeers = append(ingressPeers, peer)
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

// ObserverNetworkPolicyName returns the name for the observer NetworkPolicy.
func ObserverNetworkPolicyName(v *vkov1.Valkey) string {
	prefix := networkPolicyPrefix(v)
	return fmt.Sprintf("%s%s-observer", prefix, v.Name)
}

// BuildObserverNetworkPolicy builds the NetworkPolicy for the observer pod. It
// has no ingress rule: no deployed component connects to the observer, kubelet's
// probes come from the node, and a scraper of its /metrics is the
// administrator's to admit (ADR 0039 D2, D3). Ingress stays nil, not empty, so
// the desired spec compares equal to what the API server returns.
func BuildObserverNetworkPolicy(v *vkov1.Valkey) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObserverNetworkPolicyName(v),
			Namespace: v.Namespace,
			Labels:    ObserverLabels(v),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: ObserverSelectorLabels(v),
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
}

// NetworkPolicyHasChanged returns true if the desired NetworkPolicy differs from the current one.
// Uses reflect.DeepEqual for ingress rule comparison to correctly handle all peer types
// (PodSelector, NamespaceSelector, or combined peers).
func NetworkPolicyHasChanged(desired, current *networkingv1.NetworkPolicy) bool {
	if desired.Spec.PodSelector.String() != current.Spec.PodSelector.String() {
		return true
	}
	return !reflect.DeepEqual(desired.Spec.Ingress, current.Spec.Ingress)
}
