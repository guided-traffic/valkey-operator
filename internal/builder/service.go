package builder

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// ClientServiceName returns the name for the client-facing Service.
func ClientServiceName(v *vkov1.Valkey) string {
	return v.Name
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

// BuildClientService builds the client-facing Service that points to the master pod.
// In standalone mode, it simply selects all Valkey pods.
// In HA mode, it will be refined to select the master via label selector.
func BuildClientService(v *vkov1.Valkey) *corev1.Service {
	labels := common.BaseLabels(v, common.ComponentValkey)
	selector := common.SelectorLabels(v, common.ComponentValkey)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClientServiceName(v),
			Namespace: v.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selector,
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

// ReadServiceName returns the name for the read-only (replica) Service.
func ReadServiceName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-read", v.Name)
}
