package builder

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

const (
	// TLSVolumeName is the name of the volume for TLS certificates.
	TLSVolumeName = "tls"

	// TLSMountPath is the mount path for TLS certificates inside containers.
	TLSMountPath = "/tls"

	// TLSPort is the TLS-enabled Valkey port.
	TLSPort = 16379

	// CertManagerAPIVersion is the API version for cert-manager Certificate resources.
	CertManagerAPIVersion = "cert-manager.io/v1"

	// CertManagerCertificateKind is the kind for cert-manager Certificate resources.
	CertManagerCertificateKind = "Certificate"
)

// ValkeyCertificateName returns the name of the Certificate resource for Valkey pods.
func ValkeyCertificateName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-tls", v.Name)
}

// SentinelCertificateName returns the name of the Certificate resource for Sentinel pods.
func SentinelCertificateName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-sentinel-tls", v.Name)
}

// ValkeyTLSSecretName returns the name of the Secret that holds TLS certs for Valkey.
// When cert-manager is used, this is the Secret created by the Certificate resource.
// When a user-provided secret is used, this returns the user's secret name.
func ValkeyTLSSecretName(v *vkov1.Valkey) string {
	if v.IsTLSSecretProvided() {
		return v.Spec.TLS.SecretName
	}
	return fmt.Sprintf("%s-tls", v.Name)
}

// SentinelTLSSecretName returns the name of the Secret that holds TLS certs for Sentinel.
// When cert-manager is used, a separate Certificate is created for Sentinel.
// When a user-provided secret is used, the same secret is shared.
func SentinelTLSSecretName(v *vkov1.Valkey) string {
	if v.IsTLSSecretProvided() {
		return v.Spec.TLS.SecretName
	}
	return fmt.Sprintf("%s-sentinel-tls", v.Name)
}

// valkeyDNSNames generates the DNS names for the Valkey Certificate.
// This includes individual pod DNS names, the headless service, and the client service.
func valkeyDNSNames(v *vkov1.Valkey) []string {
	headless := common.HeadlessServiceName(v, common.ComponentValkey)
	clientSvc := ClientServiceName(v)

	var dnsNames []string

	// Individual pod DNS names.
	for i := int32(0); i < v.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", common.StatefulSetName(v, common.ComponentValkey), i)
		// Pod FQDN via headless service.
		dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, headless, v.Namespace))
		// Short pod DNS.
		dnsNames = append(dnsNames, fmt.Sprintf("%s.%s", podName, headless))
	}

	// Headless service DNS.
	dnsNames = append(dnsNames, headless)
	dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.svc.cluster.local", headless, v.Namespace))

	// Client service DNS.
	dnsNames = append(dnsNames, clientSvc)
	dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.svc.cluster.local", clientSvc, v.Namespace))

	// Read service DNS (if applicable).
	readSvc := ReadServiceName(v)
	dnsNames = append(dnsNames, readSvc)
	dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.svc.cluster.local", readSvc, v.Namespace))

	// Localhost for local connections.
	dnsNames = append(dnsNames, "localhost")

	// Extra DNS names from CRD spec.
	if v.Spec.TLS.CertManager != nil && len(v.Spec.TLS.CertManager.ExtraDNSNames) > 0 {
		dnsNames = append(dnsNames, v.Spec.TLS.CertManager.ExtraDNSNames...)
	}

	return dnsNames
}

// sentinelDNSNames generates the DNS names for the Sentinel Certificate.
func sentinelDNSNames(v *vkov1.Valkey) []string {
	headless := common.HeadlessServiceName(v, common.ComponentSentinel)

	var dnsNames []string

	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}

	// Individual pod DNS names.
	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", common.StatefulSetName(v, common.ComponentSentinel), i)
		// Pod FQDN via headless service.
		dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, headless, v.Namespace))
		// Short pod DNS.
		dnsNames = append(dnsNames, fmt.Sprintf("%s.%s", podName, headless))
	}

	// Headless service DNS.
	dnsNames = append(dnsNames, headless)
	dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.svc.cluster.local", headless, v.Namespace))

	// Localhost for local connections.
	dnsNames = append(dnsNames, "localhost")

	// Extra DNS names from CRD spec.
	if v.Spec.TLS.CertManager != nil && len(v.Spec.TLS.CertManager.ExtraDNSNames) > 0 {
		dnsNames = append(dnsNames, v.Spec.TLS.CertManager.ExtraDNSNames...)
	}

	return dnsNames
}

// BuildValkeyCertificate builds the cert-manager Certificate resource for Valkey pods.
func BuildValkeyCertificate(v *vkov1.Valkey) *unstructured.Unstructured {
	labels := common.BaseLabels(v, common.ComponentValkey)
	cm := v.Spec.TLS.CertManager

	issuerRef := map[string]interface{}{
		"name": cm.Issuer.Name,
		"kind": cm.Issuer.Kind,
	}
	if cm.Issuer.Group != "" {
		issuerRef["group"] = cm.Issuer.Group
	}

	// Convert DNS names to []interface{}.
	dnsNames := valkeyDNSNames(v)
	dnsNamesIface := make([]interface{}, len(dnsNames))
	for i, d := range dnsNames {
		dnsNamesIface[i] = d
	}

	cert := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": CertManagerAPIVersion,
			"kind":       CertManagerCertificateKind,
			"metadata": map[string]interface{}{
				"name":      ValkeyCertificateName(v),
				"namespace": v.Namespace,
				"labels":    toInterfaceMap(labels),
			},
			"spec": map[string]interface{}{
				"secretName": ValkeyTLSSecretName(v),
				"issuerRef":  issuerRef,
				"dnsNames":   dnsNamesIface,
				"usages": []interface{}{
					"server auth",
					"client auth",
				},
			},
		},
	}

	return cert
}

// BuildSentinelCertificate builds the cert-manager Certificate resource for Sentinel pods.
func BuildSentinelCertificate(v *vkov1.Valkey) *unstructured.Unstructured {
	labels := common.BaseLabels(v, common.ComponentSentinel)
	cm := v.Spec.TLS.CertManager

	issuerRef := map[string]interface{}{
		"name": cm.Issuer.Name,
		"kind": cm.Issuer.Kind,
	}
	if cm.Issuer.Group != "" {
		issuerRef["group"] = cm.Issuer.Group
	}

	// Convert DNS names to []interface{}.
	dnsNames := sentinelDNSNames(v)
	dnsNamesIface := make([]interface{}, len(dnsNames))
	for i, d := range dnsNames {
		dnsNamesIface[i] = d
	}

	cert := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": CertManagerAPIVersion,
			"kind":       CertManagerCertificateKind,
			"metadata": map[string]interface{}{
				"name":      SentinelCertificateName(v),
				"namespace": v.Namespace,
				"labels":    toInterfaceMap(labels),
			},
			"spec": map[string]interface{}{
				"secretName": SentinelTLSSecretName(v),
				"issuerRef":  issuerRef,
				"dnsNames":   dnsNamesIface,
				"usages": []interface{}{
					"server auth",
					"client auth",
				},
			},
		},
	}

	return cert
}

// CertificateOwnerRef returns an OwnerReference for setting on Certificate resources.
func CertificateOwnerRef(v *vkov1.Valkey) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: vkov1.GroupVersion.String(),
		Kind:       "Valkey",
		Name:       v.Name,
		UID:        v.UID,
	}
}

// toInterfaceMap converts a map[string]string to map[string]interface{} for unstructured objects.
func toInterfaceMap(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
