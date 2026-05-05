package builder

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// AllServiceName returns the name for the all-pods Service (<name>-all).
// This service load-balances across all Valkey pods regardless of role.
func AllServiceName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-all", v.Name)
}

// RWServiceName returns the name for the read-write Service (<name>-rw).
// This service routes only to the master pod.
func RWServiceName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-rw", v.Name)
}

// ReadOnlyServiceName returns the name for the read-only replica Service (<name>-r).
// This service routes only to replica pods.
func ReadOnlyServiceName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-r", v.Name)
}

// valkeyClientPorts returns the Service ports for a Valkey client Service.
// When TLS is disabled: single port 6379 → named port "valkey" (pod port 6379).
// When TLS is enabled:  single port 16379 → named port "valkey" (pod port 16379, TLS).
// When TLS + allowUnencrypted: above plus 6379 → named port "valkey-plain" (pod port 6379).
func valkeyClientPorts(v *vkov1.Valkey) []corev1.ServicePort {
	var primaryPort int32 = ValkeyPort
	if v.IsTLSEnabled() {
		primaryPort = TLSPort
	}
	ports := []corev1.ServicePort{
		{
			Name:       ValkeyContainerName,
			Port:       primaryPort,
			TargetPort: intstr.FromString(ValkeyContainerName),
			Protocol:   corev1.ProtocolTCP,
		},
	}
	if v.IsValkeyUnencryptedAllowed() {
		ports = append(ports, corev1.ServicePort{
			Name:       ValkeyPlainContainerName,
			Port:       ValkeyPort,
			TargetPort: intstr.FromString(ValkeyPlainContainerName),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return ports
}

// sentinelHeadlessPorts returns the Service ports for the Sentinel headless Service.
// When TLS is disabled: single port 26379.
// When TLS is enabled:  single port 36379 (= SentinelPort + 10000).
// When TLS + sentinel.allowUnencrypted: above plus 26379 → "sentinel-plain".
func sentinelHeadlessPorts(v *vkov1.Valkey) []corev1.ServicePort {
	var primaryPort int32 = SentinelPort
	if v.IsTLSEnabled() {
		primaryPort = SentinelTLSPort
	}
	ports := []corev1.ServicePort{
		{
			Name:       SentinelContainerName,
			Port:       primaryPort,
			TargetPort: intstr.FromString(SentinelContainerName),
			Protocol:   corev1.ProtocolTCP,
		},
	}
	if v.IsSentinelUnencryptedAllowed() {
		ports = append(ports, corev1.ServicePort{
			Name:       "sentinel-plain",
			Port:       SentinelPort,
			TargetPort: intstr.FromString("sentinel-plain"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return ports
}

// BuildHeadlessService builds the headless Service for StatefulSet DNS resolution.
// The headless service is internal infrastructure only; it always exposes the primary
// Valkey port for DNS record generation and does not expose a plain port.
func BuildHeadlessService(v *vkov1.Valkey) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.HeadlessServiceName(v, common.ComponentValkey),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Spec: corev1.ServiceSpec{
			Type:                     corev1.ServiceTypeClusterIP,
			ClusterIP:                corev1.ClusterIPNone,
			Selector:                 common.SelectorLabels(v, common.ComponentValkey),
			PublishNotReadyAddresses: true,
			Ports: []corev1.ServicePort{
				{
					Name:       ValkeyContainerName,
					Port:       ValkeyPort,
					TargetPort: intstr.FromString(ValkeyContainerName),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildRWService builds the read-write Service that routes only to the master pod.
// The selector requires instanceRole=master, which is managed by the sidecar container.
// When TLS is enabled the primary port is 16379; when allowUnencrypted is also set,
// port 6379 is added as "valkey-plain".
func BuildRWService(v *vkov1.Valkey) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RWServiceName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: common.MasterSelectorLabels(v),
			Ports:    valkeyClientPorts(v),
		},
	}
}

// BuildAllService builds the all-pods Service that load-balances across all Valkey pods.
// Useful for read-heavy workloads where reads from replicas are acceptable.
// Port rules follow the same TLS / allowUnencrypted logic as BuildRWService.
func BuildAllService(v *vkov1.Valkey) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AllServiceName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: common.SelectorLabels(v, common.ComponentValkey),
			Ports:    valkeyClientPorts(v),
		},
	}
}

// BuildReadOnlyService builds a read-only Service that routes only to replica pods.
// The selector requires instanceRole=replica, managed by the sidecar container.
// Only created in multi-replica mode.
// Port rules follow the same TLS / allowUnencrypted logic as BuildRWService.
func BuildReadOnlyService(v *vkov1.Valkey) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ReadOnlyServiceName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: common.ReplicaSelectorLabels(v),
			Ports:    valkeyClientPorts(v),
		},
	}
}

// BuildSentinelHeadlessService builds the headless Service for Sentinel StatefulSet DNS resolution.
// When TLS is enabled the primary port is 36379 (= SentinelPort + 10000); when
// sentinel.allowUnencrypted is also set, port 26379 is added as "sentinel-plain".
func BuildSentinelHeadlessService(v *vkov1.Valkey) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.HeadlessServiceName(v, common.ComponentSentinel),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentSentinel),
		},
		Spec: corev1.ServiceSpec{
			Type:                     corev1.ServiceTypeClusterIP,
			ClusterIP:                corev1.ClusterIPNone,
			Selector:                 common.SelectorLabels(v, common.ComponentSentinel),
			PublishNotReadyAddresses: true,
			Ports:                    sentinelHeadlessPorts(v),
		},
	}
}
