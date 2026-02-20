package health

import (
	"context"
	"crypto/tls"
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
	c := checker.newValkeyClient("localhost:6379", nil)
	assert.NotNil(t, c)
}

func TestNewValkeyClient_WithTLS(t *testing.T) {
	checker := NewChecker(nil)
	c := checker.newValkeyClient("localhost:16379", &tls.Config{MinVersion: tls.VersionTLS12})
	assert.NotNil(t, c)
}
