package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

func newTestValkey(name, ns string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			Sentinel: &vkov1.SentinelSpec{
				Enabled:  true,
				Replicas: 3,
			},
		},
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// --- PodAddressForComponent ---

func TestPodAddressForComponent_Valkey(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := PodAddressForComponent(v, "test-0", common.ComponentValkey, builder.ValkeyPort)

	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local:6379", addr)
}

func TestPodAddressForComponent_Sentinel(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := PodAddressForComponent(v, "test-sentinel-0", common.ComponentSentinel, builder.SentinelPort)

	assert.Equal(t, "test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379", addr)
}

func TestPodAddressForComponent_CustomNamespace(t *testing.T) {
	v := newTestValkey("myvalkey", "production")
	addr := PodAddressForComponent(v, "myvalkey-1", common.ComponentValkey, builder.ValkeyPort)

	assert.Equal(t, "myvalkey-1.myvalkey-headless.production.svc.cluster.local:6379", addr)
}

// --- NewChecker ---

func TestNewChecker(t *testing.T) {
	c := NewChecker(nil)
	assert.NotNil(t, c)
}

// --- ClusterState ---

func TestClusterState_Defaults(t *testing.T) {
	state := &ClusterState{}
	assert.Equal(t, "", state.MasterPod)
	assert.Equal(t, int32(0), state.ReadyReplicas)
	assert.False(t, state.AllSynced)
	assert.False(t, state.SentinelMonitoring)
	assert.Nil(t, state.Error)
}

// --- buildTLSConfig ---

// testCACert is a self-signed CA certificate for testing purposes only.
const testCACert = `-----BEGIN CERTIFICATE-----
MIIBejCCAR+gAwIBAgIUS2/Z6nko0KrjmZ0isXIKpnW9gaMwCgYIKoZIzj0EAwIw
EjEQMA4GA1UECgwHQWNtZSBDbzAeFw0yNjAyMTkxNTI1NThaFw0zNjAyMTcxNTI1
NThaMBIxEDAOBgNVBAoMB0FjbWUgQ28wWTATBgcqhkjOPQIBBggqhkjOPQMBBwNC
AARIjEAmZv4pCmau7ruKl2JZHwl2MjolHJYy7lxhkLw7TWfj8iX7Fxnhlz0BXZqP
oF7ek0Fxvw7p60NYXxWjwkxZo1MwUTAdBgNVHQ4EFgQU48l9XI8AgN3399I9KLB1
D7y4XccwHwYDVR0jBBgwFoAU48l9XI8AgN3399I9KLB1D7y4XccwDwYDVR0TAQH/
BAUwAwEB/zAKBggqhkjOPQQDAgNJADBGAiEA/M1Mw9nDZg7HKX8NxL+GZy8KSvOp
HZpATeWHjH8TsQ0CIQCkTrqAe9DpBTdPlF6f9kyUkVLtXiMjb6KTTH9m8x3Zzg==
-----END CERTIFICATE-----`

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = vkov1.AddToScheme(s)
	return s
}

func TestBuildTLSConfig_TLSDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	// No TLS configured — should return nil.

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	checker := NewChecker(fakeClient)

	tlsConfig, err := checker.buildTLSConfig(context.Background(), v, "test-tls")
	require.NoError(t, err)
	assert.Nil(t, tlsConfig, "TLS config should be nil when TLS is disabled")
}

func TestBuildTLSConfig_TLSEnabled_WithValidCA(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "test-issuer",
				},
			},
		}
	})

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tls",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt":  []byte(testCACert),
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(tlsSecret).Build()
	checker := NewChecker(fakeClient)

	tlsConfig, err := checker.buildTLSConfig(context.Background(), v, "test-tls")
	require.NoError(t, err)
	assert.NotNil(t, tlsConfig, "TLS config should be non-nil when TLS is enabled")
	assert.NotNil(t, tlsConfig.RootCAs, "RootCAs should be set")
}

func TestBuildTLSConfig_TLSEnabled_SecretMissing(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "test-issuer",
				},
			},
		}
	})

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	checker := NewChecker(fakeClient)

	_, err := checker.buildTLSConfig(context.Background(), v, "test-tls")
	assert.Error(t, err, "should error when TLS secret is missing")
	assert.Contains(t, err.Error(), "reading TLS secret")
}

func TestBuildTLSConfig_TLSEnabled_MissingCACert(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "test-issuer",
				},
			},
		}
	})

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tls",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(tlsSecret).Build()
	checker := NewChecker(fakeClient)

	_, err := checker.buildTLSConfig(context.Background(), v, "test-tls")
	assert.Error(t, err, "should error when ca.crt is missing from secret")
	assert.Contains(t, err.Error(), "missing ca.crt")
}

// --- newValkeyClient ---

func TestNewValkeyClient_PlainTCP(t *testing.T) {
	checker := NewChecker(nil)
	c := checker.newValkeyClient("localhost:6379", "", nil)
	assert.NotNil(t, c)
}

func TestNewValkeyClient_WithTLS(t *testing.T) {
	checker := NewChecker(nil)
	c := checker.newValkeyClient("localhost:16379", "", &tls.Config{MinVersion: tls.VersionTLS12})
	assert.NotNil(t, c)
}

// --- newValkeyClient edge cases ---

func TestNewValkeyClient_WithPassword(t *testing.T) {
	checker := NewChecker(nil)
	c := checker.newValkeyClient("localhost:6379", "secret", nil)
	assert.NotNil(t, c)
}

func TestNewValkeyClient_WithTLSAndPassword(t *testing.T) {
	checker := NewChecker(nil)
	c := checker.newValkeyClient("localhost:16379", "secret", &tls.Config{MinVersion: tls.VersionTLS12})
	assert.NotNil(t, c)
}

// --- buildTLSConfig edge cases ---

func TestBuildTLSConfig_InvalidCACert(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "test-issuer",
				},
			},
		}
	})

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tls",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt":  []byte("not-a-valid-pem-certificate"),
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(tlsSecret).Build()
	checker := NewChecker(fakeClient)

	_, err := checker.buildTLSConfig(context.Background(), v, "test-tls")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse CA certificate")
}

func TestBuildTLSConfig_EmptyCACert(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "test-issuer",
				},
			},
		}
	})

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tls",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt":  []byte(""),
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(tlsSecret).Build()
	checker := NewChecker(fakeClient)

	_, err := checker.buildTLSConfig(context.Background(), v, "test-tls")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse CA certificate")
}

func TestBuildTLSConfig_ValidCA_MinVersionTLS12(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{
					Kind: "ClusterIssuer",
					Name: "test-issuer",
				},
			},
		}
	})

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tls",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt":  []byte(testCACert),
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(tlsSecret).Build()
	checker := NewChecker(fakeClient)

	tlsConfig, err := checker.buildTLSConfig(context.Background(), v, "test-tls")
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS12), tlsConfig.MinVersion)
}

// --- readAuthPassword edge cases ---

func TestReadAuthPassword_AuthDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	// No auth configured in default test valkey.
	v.Spec.Auth = nil

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	checker := NewChecker(fakeClient)

	password := checker.readAuthPassword(context.Background(), v)
	assert.Equal(t, "", password)
}

func TestReadAuthPassword_SecretMissing(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "nonexistent-secret",
			SecretPasswordKey: "password",
		}
	})

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	checker := NewChecker(fakeClient)

	password := checker.readAuthPassword(context.Background(), v)
	assert.Equal(t, "", password, "missing secret should return empty string")
}

func TestReadAuthPassword_SecretPresent(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("my-password-123"),
		},
	}

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	checker := NewChecker(fakeClient)

	password := checker.readAuthPassword(context.Background(), v)
	assert.Equal(t, "my-password-123", password)
}

func TestReadAuthPassword_SecretWrongKey(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "wrong-key",
		}
	})

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("my-password"),
		},
	}

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	checker := NewChecker(fakeClient)

	password := checker.readAuthPassword(context.Background(), v)
	assert.Equal(t, "", password, "wrong key should return empty")
}

func TestReadAuthPassword_EmptySecretName(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "",
			SecretPasswordKey: "password",
		}
	})

	s := testScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	checker := NewChecker(fakeClient)

	password := checker.readAuthPassword(context.Background(), v)
	assert.Equal(t, "", password, "empty secret name means auth not enabled")
}

// --- podAddress edge cases ---

func TestPodAddress_Valkey(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := podAddress(v, "test-0", 6379)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local:6379", addr)
}

func TestPodAddress_Sentinel(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := podAddress(v, "test-sentinel-0", 26379)
	assert.Equal(t, "test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379", addr)
}

func TestPodAddress_ShortPodName(t *testing.T) {
	// Pod name shorter than 10 chars — sentinel detection should not panic.
	v := newTestValkey("t", "ns")
	addr := podAddress(v, "t-0", 6379)
	assert.Contains(t, addr, "t-0.")
	assert.Contains(t, addr, ".ns.svc.cluster.local:6379")
}

func TestPodAddress_TLSPort(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := podAddress(v, "test-0", 16379)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local:16379", addr)
}

// --- PodAddressForComponent edge cases ---

func TestPodAddressForComponent_TLSPort(t *testing.T) {
	v := newTestValkey("test", "default")
	valkeyTLSPort := builder.ValkeyPort + 10000 // 16379
	addr := PodAddressForComponent(v, "test-0", common.ComponentValkey, valkeyTLSPort)
	assert.Contains(t, addr, fmt.Sprintf(":%d", valkeyTLSPort))
}

func TestPodAddressForComponent_SentinelTLSPort(t *testing.T) {
	v := newTestValkey("test", "default")
	addr := PodAddressForComponent(v, "test-sentinel-0", common.ComponentSentinel, builder.SentinelTLSPort)
	assert.Contains(t, addr, fmt.Sprintf(":%d", builder.SentinelTLSPort))
}

// --- ClusterState edge cases ---

func TestClusterState_WithError(t *testing.T) {
	state := &ClusterState{
		Error: fmt.Errorf("master not found"),
	}
	assert.NotNil(t, state.Error)
	assert.Equal(t, "", state.MasterPod)
	assert.Equal(t, int32(0), state.ReadyReplicas)
}

func TestClusterState_AllSynced(t *testing.T) {
	state := &ClusterState{
		MasterPod:     "test-0",
		ReadyReplicas: 2,
		TotalReplicas: 2,
		AllSynced:     true,
	}
	assert.True(t, state.AllSynced)
	assert.Equal(t, state.ReadyReplicas, state.TotalReplicas)
}

func TestClusterState_PartiallyReady(t *testing.T) {
	state := &ClusterState{
		MasterPod:          "test-0",
		ReadyReplicas:      1,
		TotalReplicas:      2,
		AllSynced:          false,
		SentinelMonitoring: true,
	}
	assert.False(t, state.AllSynced)
	assert.True(t, state.SentinelMonitoring)
}

// --- masterCandidateNames ---

func TestMasterCandidateNames(t *testing.T) {
	candidates := []masterCandidate{
		{podName: "test-0", addr: "test-0:6379", connectedSlaves: 0},
		{podName: "test-1", addr: "test-1:6379", connectedSlaves: 2},
	}
	names := masterCandidateNames(candidates)
	assert.Equal(t, []string{"test-0", "test-1"}, names)
}

func TestMasterCandidateNames_Empty(t *testing.T) {
	names := masterCandidateNames(nil)
	assert.Empty(t, names)
}

func TestMasterCandidateNames_Single(t *testing.T) {
	candidates := []masterCandidate{
		{podName: "test-0", addr: "test-0:6379", connectedSlaves: 1},
	}
	names := masterCandidateNames(candidates)
	assert.Equal(t, []string{"test-0"}, names)
}
