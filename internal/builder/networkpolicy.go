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

// BuildValkeyNetworkPolicy builds the NetworkPolicy that allows Valkey↔Valkey
// and Sentinel→Valkey traffic within the cluster.
// It restricts ingress to the Valkey port from other Valkey pods, Sentinel pods,
// and (when operatorNamespace is non-empty) all pods in the operator namespace
// so the operator can reach Valkey pods for health checks (e.g. INFO replication).
// It unconditionally allows ingress on the sidecar health port from all sources
// so that kubelet readiness/liveness probes always succeed.
func BuildValkeyNetworkPolicy(v *vkov1.Valkey, operatorNamespace string) *networkingv1.NetworkPolicy {
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

	// Allow ingress from the operator namespace so the operator can reach Valkey
	// pods for health checks (INFO replication). Uses the standard
	// kubernetes.io/metadata.name namespace label (available since Kubernetes 1.21).
	if operatorNamespace != "" {
		ingressPeers = append(ingressPeers, networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": operatorNamespace,
				},
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

	// Always allow ingress on the sidecar health port from all sources.
	// Kubelet readiness/liveness probes originate from the node (host network),
	// which cannot be matched by a pod selector, so no From restriction is applied.
	healthPort := intstr.FromInt32(SidecarHealthPort)
	ingressRules = append(ingressRules, networkingv1.NetworkPolicyIngressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{
				Protocol: &tcpProtocol,
				Port:     &healthPort,
			},
		},
	})

	// When the metrics exporter is enabled, allow ingress on the exporter port.
	// The scrape traffic originates from Prometheus pods whose location is not
	// known to the operator, so — like the health port — no From restriction is
	// applied. The exporter endpoint is read-only.
	if v.IsMetricsEnabled() {
		metricsPort := intstr.FromInt32(v.MetricsPort())
		ingressRules = append(ingressRules, networkingv1.NetworkPolicyIngressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: &tcpProtocol,
					Port:     &metricsPort,
				},
			},
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
// It restricts ingress to the Sentinel port from Valkey and Sentinel pods, and
// (when operatorNamespace is non-empty) also from all pods in the operator namespace
// so the operator can reach Sentinel pods for health checks.
func BuildSentinelNetworkPolicy(v *vkov1.Valkey, operatorNamespace string) *networkingv1.NetworkPolicy {
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

	// Allow ingress from the operator namespace so the operator can reach Sentinel
	// pods for health checks (SENTINEL MASTER).
	if operatorNamespace != "" {
		ingressPeers = append(ingressPeers, networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": operatorNamespace,
				},
			},
		})
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

// BuildObserverNetworkPolicy builds the NetworkPolicy for the observer pod.
// It only allows ingress on the health port (8084) from all sources for kubelet probes.
func BuildObserverNetworkPolicy(v *vkov1.Valkey) *networkingv1.NetworkPolicy {
	labels := ObserverLabels(v)
	observerSelector := ObserverSelectorLabels(v)

	tcpProtocol := corev1.ProtocolTCP
	healthPort := intstr.FromInt32(ObserverHealthPort)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObserverNetworkPolicyName(v),
			Namespace: v.Namespace,
			Labels:    labels,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: observerSelector,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &tcpProtocol,
							Port:     &healthPort,
						},
					},
				},
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
