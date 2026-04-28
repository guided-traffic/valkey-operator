package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// --- Certificate Name Helpers ---

func TestValkeyCertificateName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-tls", ValkeyCertificateName(v))
}

func TestSentinelCertificateName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-sentinel-tls", SentinelCertificateName(v))
}

// --- TLS Secret Name Helpers ---

func TestValkeyTLSSecretName_CertManager(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "ca",
				},
			},
		}
	})
	assert.Equal(t, "test-tls", ValkeyTLSSecretName(v))
}

func TestValkeyTLSSecretName_UserProvided(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:    true,
			SecretName: "my-custom-tls-secret",
		}
	})
	assert.Equal(t, "my-custom-tls-secret", ValkeyTLSSecretName(v))
}

func TestSentinelTLSSecretName_CertManager(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "ca",
				},
			},
		}
	})
	assert.Equal(t, "test-sentinel-tls", SentinelTLSSecretName(v))
}

func TestSentinelTLSSecretName_UserProvided(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:    true,
			SecretName: "my-custom-tls-secret",
		}
	})
	assert.Equal(t, "my-custom-tls-secret", SentinelTLSSecretName(v))
}

func TestSentinelTLSSecretName_UnifiedCertificate(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:            true,
			UnifiedCertificate: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})
	// In unified mode, Sentinel reuses the Valkey TLS Secret.
	assert.Equal(t, ValkeyTLSSecretName(v), SentinelTLSSecretName(v))
	assert.Equal(t, "test-tls", SentinelTLSSecretName(v))
}

// --- BuildValkeyCertificate ---

func TestBuildValkeyCertificate_Basic(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "prod"
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Group: "cert-manager.io",
					Kind:  "ClusterIssuer",
					Name:  "cluster-ca",
				},
			},
		}
	})

	cert := BuildValkeyCertificate(v)

	// Metadata.
	assert.Equal(t, "test-tls", cert.GetName())
	assert.Equal(t, "prod", cert.GetNamespace())
	assert.Equal(t, "cert-manager.io/v1", cert.GetAPIVersion())
	assert.Equal(t, "Certificate", cert.GetKind())

	// Labels.
	labels := cert.GetLabels()
	assert.Equal(t, "valkey", labels["app.kubernetes.io/component"])
	assert.Equal(t, "test", labels["app.kubernetes.io/instance"])

	// Spec.
	spec, ok := cert.Object["spec"].(map[string]interface{})
	require.True(t, ok)

	// Secret name.
	assert.Equal(t, "test-tls", spec["secretName"])

	// Issuer ref.
	issuerRef, ok := spec["issuerRef"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "cluster-ca", issuerRef["name"])
	assert.Equal(t, "ClusterIssuer", issuerRef["kind"])
	assert.Equal(t, "cert-manager.io", issuerRef["group"])

	// DNS names.
	dnsNames, ok := spec["dnsNames"].([]interface{})
	require.True(t, ok)

	dnsNamesList := make([]string, len(dnsNames))
	for i, d := range dnsNames {
		dnsNamesList[i] = d.(string)
	}

	// Should contain pod FQDNs for 3 replicas.
	assert.Contains(t, dnsNamesList, "test-0.test-headless.prod.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "test-1.test-headless.prod.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "test-2.test-headless.prod.svc.cluster.local")

	// Should contain headless service.
	assert.Contains(t, dnsNamesList, "test-headless")
	assert.Contains(t, dnsNamesList, "test-headless.prod.svc.cluster.local")

	// Should contain read-write service.
	assert.Contains(t, dnsNamesList, "test-rw")
	assert.Contains(t, dnsNamesList, "test-rw.prod.svc.cluster.local")

	// Should contain all-pods service.
	assert.Contains(t, dnsNamesList, "test-all")
	assert.Contains(t, dnsNamesList, "test-all.prod.svc.cluster.local")

	// Should contain read-only replica service (3 replicas).
	assert.Contains(t, dnsNamesList, "test-r")
	assert.Contains(t, dnsNamesList, "test-r.prod.svc.cluster.local")
	// Should contain localhost.
	assert.Contains(t, dnsNamesList, "localhost")

	// Usages.
	usages, ok := spec["usages"].([]interface{})
	require.True(t, ok)
	assert.Contains(t, usages, "server auth")
	assert.Contains(t, usages, "client auth")
}

func TestBuildValkeyCertificate_ExtraDNSNames(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "default"
		v.Spec.Replicas = 1
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "Issuer",
					Name: "my-issuer",
				},
				ExtraDNSNames: []string{
					"valkey.example.com",
					"cache.internal.company.io",
				},
			},
		}
	})

	cert := BuildValkeyCertificate(v)

	spec := cert.Object["spec"].(map[string]interface{})
	dnsNames := spec["dnsNames"].([]interface{})

	dnsNamesList := make([]string, len(dnsNames))
	for i, d := range dnsNames {
		dnsNamesList[i] = d.(string)
	}

	// Should contain extra DNS names.
	assert.Contains(t, dnsNamesList, "valkey.example.com")
	assert.Contains(t, dnsNamesList, "cache.internal.company.io")

	// Should still contain standard DNS names.
	assert.Contains(t, dnsNamesList, "test-0.test-headless.default.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "localhost")
}

func TestBuildValkeyCertificate_IssuerWithoutGroup(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "Issuer",
					Name: "local-issuer",
					// Group intentionally left empty.
				},
			},
		}
	})

	cert := BuildValkeyCertificate(v)

	spec := cert.Object["spec"].(map[string]interface{})
	issuerRef := spec["issuerRef"].(map[string]interface{})

	// Group should not be present when empty.
	_, hasGroup := issuerRef["group"]
	assert.False(t, hasGroup)
}

// --- BuildSentinelCertificate ---

func TestBuildSentinelCertificate_Basic(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "prod"
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Group: "cert-manager.io",
					Kind:  "ClusterIssuer",
					Name:  "cluster-ca",
				},
			},
		}
	})

	cert := BuildSentinelCertificate(v)

	// Metadata.
	assert.Equal(t, "test-sentinel-tls", cert.GetName())
	assert.Equal(t, "prod", cert.GetNamespace())

	// Labels.
	labels := cert.GetLabels()
	assert.Equal(t, "sentinel", labels["app.kubernetes.io/component"])

	// Spec.
	spec := cert.Object["spec"].(map[string]interface{})
	assert.Equal(t, "test-sentinel-tls", spec["secretName"])

	// DNS names.
	dnsNames := spec["dnsNames"].([]interface{})
	dnsNamesList := make([]string, len(dnsNames))
	for i, d := range dnsNames {
		dnsNamesList[i] = d.(string)
	}

	// Should contain sentinel pod FQDNs.
	assert.Contains(t, dnsNamesList, "test-sentinel-0.test-sentinel-headless.prod.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "test-sentinel-1.test-sentinel-headless.prod.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "test-sentinel-2.test-sentinel-headless.prod.svc.cluster.local")

	// Should contain sentinel headless service.
	assert.Contains(t, dnsNamesList, "test-sentinel-headless")
	assert.Contains(t, dnsNamesList, "test-sentinel-headless.prod.svc.cluster.local")

	// Should contain localhost.
	assert.Contains(t, dnsNamesList, "localhost")
}

func TestBuildSentinelCertificate_ExtraDNSNames(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "ca",
				},
				ExtraDNSNames: []string{
					"sentinel.example.com",
				},
			},
		}
	})

	cert := BuildSentinelCertificate(v)

	spec := cert.Object["spec"].(map[string]interface{})
	dnsNames := spec["dnsNames"].([]interface{})
	dnsNamesList := make([]string, len(dnsNames))
	for i, d := range dnsNames {
		dnsNamesList[i] = d.(string)
	}

	assert.Contains(t, dnsNamesList, "sentinel.example.com")
}

// --- BuildValkeyCertificate (unified mode) ---

func TestBuildValkeyCertificate_Unified_IncludesSentinelDNSNames(t *testing.T) {
	v := newTestValkey("oauth2-valkey", func(v *vkov1.Valkey) {
		v.Namespace = "iam"
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:            true,
			UnifiedCertificate: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})

	cert := BuildValkeyCertificate(v)
	spec := cert.Object["spec"].(map[string]interface{})
	dnsNamesList := stringsFromIface(spec["dnsNames"].([]interface{}))

	// Valkey-side hostnames must still be present.
	assert.Contains(t, dnsNamesList, "oauth2-valkey-0.oauth2-valkey-headless.iam.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "oauth2-valkey-headless")
	assert.Contains(t, dnsNamesList, "oauth2-valkey-rw")

	// Sentinel-side hostnames must be merged into the Valkey cert.
	assert.Contains(t, dnsNamesList, "oauth2-valkey-sentinel-0.oauth2-valkey-sentinel-headless.iam.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "oauth2-valkey-sentinel-1.oauth2-valkey-sentinel-headless.iam.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "oauth2-valkey-sentinel-2.oauth2-valkey-sentinel-headless.iam.svc.cluster.local")
	assert.Contains(t, dnsNamesList, "oauth2-valkey-sentinel-headless")
	assert.Contains(t, dnsNamesList, "oauth2-valkey-sentinel-headless.iam.svc.cluster.local")

	// localhost present exactly once (dedupe must collapse the duplicate from
	// the merged Sentinel-only list).
	count := 0
	for _, n := range dnsNamesList {
		if n == "localhost" {
			count++
		}
	}
	assert.Equal(t, 1, count, "localhost should appear exactly once after merge+dedupe")

	// Secret name remains the Valkey TLS secret.
	assert.Equal(t, "oauth2-valkey-tls", spec["secretName"])
}

func TestBuildValkeyCertificate_Unified_NoSentinel_BehavesLikeDefault(t *testing.T) {
	// With Sentinel disabled, unified mode should not change the Valkey cert
	// (no Sentinel hostnames available to merge).
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:            true,
			UnifiedCertificate: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})

	cert := BuildValkeyCertificate(v)
	spec := cert.Object["spec"].(map[string]interface{})
	dnsNamesList := stringsFromIface(spec["dnsNames"].([]interface{}))

	for _, n := range dnsNamesList {
		assert.NotContains(t, n, "sentinel", "no sentinel names expected when Sentinel is disabled")
	}
}

func TestBuildValkeyCertificate_Unified_MergesExtraDNSNamesOnce(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:            true,
			UnifiedCertificate: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer:        vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
				ExtraDNSNames: []string{"valkey.example.com"},
			},
		}
	})

	cert := BuildValkeyCertificate(v)
	spec := cert.Object["spec"].(map[string]interface{})
	dnsNamesList := stringsFromIface(spec["dnsNames"].([]interface{}))

	count := 0
	for _, n := range dnsNamesList {
		if n == "valkey.example.com" {
			count++
		}
	}
	assert.Equal(t, 1, count, "extra DNS names must not be duplicated by the merge")
}

func stringsFromIface(in []interface{}) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = v.(string)
	}
	return out
}

// --- toInterfaceMap ---

func TestToInterfaceMap(t *testing.T) {
	input := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	result := toInterfaceMap(input)

	assert.Equal(t, "value1", result["key1"])
	assert.Equal(t, "value2", result["key2"])
	assert.Len(t, result, 2)
}

func TestToInterfaceMap_Empty(t *testing.T) {
	result := toInterfaceMap(map[string]string{})
	assert.Empty(t, result)
}

// --- CertificateOwnerRef ---

func TestCertificateOwnerRef(t *testing.T) {
	v := newTestValkey("test")
	v.UID = "test-uid-12345"

	ref := CertificateOwnerRef(v)

	assert.Equal(t, "vko.gtrfc.com/v1", ref.APIVersion)
	assert.Equal(t, "Valkey", ref.Kind)
	assert.Equal(t, "test", ref.Name)
	assert.Equal(t, "test-uid-12345", string(ref.UID))
}
