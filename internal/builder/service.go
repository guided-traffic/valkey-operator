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

// BuildHeadlessService builds the headless Service for StatefulSet DNS resolution.
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
					Name:       "valkey",
					Port:       ValkeyPort,
					TargetPort: intstr.FromString("valkey"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildRWService builds the read-write Service that routes only to the master pod.
// The selector requires instanceRole=master, which is managed by the sidecar container.
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
			Ports: []corev1.ServicePort{
				{
					Name:       "valkey",
					Port:       ValkeyPort,
					TargetPort: intstr.FromString("valkey"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildAllService builds the all-pods Service that load-balances across all Valkey pods.
// Useful for read-heavy workloads where reads from replicas are acceptable.
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
			Ports: []corev1.ServicePort{
				{
					Name:       "valkey",
					Port:       ValkeyPort,
					TargetPort: intstr.FromString("valkey"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildReadOnlyService builds a read-only Service that routes only to replica pods.
// The selector requires instanceRole=replica, managed by the sidecar container.
// Only created in multi-replica mode.
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
			Ports: []corev1.ServicePort{
				{
					Name:       "valkey",
					Port:       ValkeyPort,
					TargetPort: intstr.FromString("valkey"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildSentinelHeadlessService builds the headless Service for Sentinel StatefulSet DNS resolution.
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
			Ports: []corev1.ServicePort{
				{
					Name:       "sentinel",
					Port:       26379,
					TargetPort: intstr.FromInt32(26379),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}
