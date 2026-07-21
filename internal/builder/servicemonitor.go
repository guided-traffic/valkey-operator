package builder

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

const (
	// ServiceMonitorGroup is the API group of the Prometheus-Operator ServiceMonitor CRD.
	ServiceMonitorGroup = "monitoring.coreos.com"
	// ServiceMonitorVersion is the API version of the ServiceMonitor CRD.
	ServiceMonitorVersion = "v1"
	// ServiceMonitorKind is the kind of the ServiceMonitor CRD.
	ServiceMonitorKind = "ServiceMonitor"
)

// ServiceMonitorGVK returns the GroupVersionKind of the ServiceMonitor CRD.
func ServiceMonitorGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   ServiceMonitorGroup,
		Version: ServiceMonitorVersion,
		Kind:    ServiceMonitorKind,
	}
}

// ServiceMonitorName returns the name for the ServiceMonitor (<name>-metrics).
func ServiceMonitorName(v *vkov1.Valkey) string {
	return MetricsServiceName(v)
}

// ServiceMonitorOwnerRef returns the owner reference pointing back to the Valkey CR.
func ServiceMonitorOwnerRef(v *vkov1.Valkey) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: vkov1.GroupVersion.String(),
		Kind:       "Valkey",
		Name:       v.Name,
		UID:        v.UID,
	}
}

// BuildServiceMonitor builds a Prometheus-Operator ServiceMonitor as an
// unstructured object, so the operator needs no typed dependency on
// prometheus-operator (mirroring the cert-manager Certificate handling). It
// selects the dedicated metrics Service via the MetricsServiceLabel marker and
// scrapes its named "metrics" port.
func BuildServiceMonitor(v *vkov1.Valkey) *unstructured.Unstructured {
	labels := common.BaseLabels(v, common.ComponentValkey)
	if v.Spec.Metrics != nil && v.Spec.Metrics.ServiceMonitor != nil {
		labels = common.MergeLabels(labels, v.Spec.Metrics.ServiceMonitor.Labels)
	}

	endpoint := map[string]interface{}{
		"port":     ExporterPortName,
		"path":     "/metrics",
		"interval": v.MetricsScrapeInterval(),
		"scheme":   "http",
	}
	if v.Spec.Metrics != nil && v.Spec.Metrics.ServiceMonitor != nil &&
		v.Spec.Metrics.ServiceMonitor.ScrapeTimeout != "" {
		endpoint["scrapeTimeout"] = v.Spec.Metrics.ServiceMonitor.ScrapeTimeout
	}

	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(ServiceMonitorGVK())
	sm.SetName(ServiceMonitorName(v))
	sm.SetNamespace(v.Namespace)
	sm.SetLabels(labels)
	sm.Object["spec"] = map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": stringMapToInterface(metricsServiceSelector(v)),
		},
		"namespaceSelector": map[string]interface{}{
			"matchNames": []interface{}{v.Namespace},
		},
		"endpoints": []interface{}{endpoint},
	}
	return sm
}

// stringMapToInterface converts a map[string]string to map[string]interface{}
// so it can be embedded in an unstructured object's spec.
func stringMapToInterface(in map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, val := range in {
		out[k] = val
	}
	return out
}
