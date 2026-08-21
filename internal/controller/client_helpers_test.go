package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// --- TLS test material ---

// selfSignedCA mints a self-signed certificate valid for 127.0.0.1 and returns
// the CA PEM (as it would sit in a cert-manager Secret under "ca.crt") together
// with the server keypair. Real material is needed because buildTLSConfig is
// only meaningfully tested by completing a handshake against it.
func selfSignedCA(t *testing.T, commonName string) (caPEM []byte, serverCert tls.Certificate) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	serverCert, err = tls.X509KeyPair(caPEM, keyPEM)
	require.NoError(t, err)
	return caPEM, serverCert
}

// wireCapture is what a fake Valkey server saw on one connection.
type wireCapture struct {
	data       string
	tlsVersion uint16
}

// recordingValkeyServer starts a minimal RESP server that answers AUTH with +OK
// and PING with +PONG, and reports everything it read on the connection. When
// serverTLS is non-nil the connection is TLS-wrapped, and the negotiated version
// is reported too, so a test can prove the client really used TLS.
func capturingValkeyServer(t *testing.T, serverTLS *tls.Config) (string, <-chan wireCapture) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	captures := make(chan wireCapture, 4)

	go func() {
		for {
			raw, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go serveCapturedConnection(raw, serverTLS, captures)
		}
	}()

	return ln.Addr().String(), captures
}

func serveCapturedConnection(raw net.Conn, serverTLS *tls.Config, captures chan<- wireCapture) {
	defer func() { _ = raw.Close() }()

	conn := raw
	var version uint16
	if serverTLS != nil {
		tconn := tls.Server(raw, serverTLS)
		if err := tconn.Handshake(); err != nil {
			captures <- wireCapture{data: "handshake failed: " + err.Error()}
			return
		}
		version = tconn.ConnectionState().Version
		conn = tconn
	}

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var seen strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := conn.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			seen.WriteString(chunk)
			if strings.Contains(strings.ToUpper(chunk), "PING") {
				_, _ = conn.Write([]byte("+PONG\r\n"))
				captures <- wireCapture{data: seen.String(), tlsVersion: version}
				return
			}
			_, _ = conn.Write([]byte("+OK\r\n"))
		}
		if readErr != nil {
			captures <- wireCapture{data: seen.String(), tlsVersion: version}
			return
		}
	}
}

func awaitCapture(t *testing.T, captures <-chan wireCapture) wireCapture {
	t.Helper()
	select {
	case c := <-captures:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("the fake Valkey server recorded nothing")
		return wireCapture{}
	}
}

// --- newValkeyClient ---

// TestNewValkeyClient_UsesTLSAndAuthAsConfigured drives each of the four client
// shapes against a real socket and asserts what actually went over the wire:
// whether the connection was TLS and whether AUTH preceded the command.
func TestNewValkeyClient_UsesTLSAndAuthAsConfigured(t *testing.T) {
	caPEM, serverCert := selfSignedCA(t, "valkey-test-ca")
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEM))
	clientTLS := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	serverTLS := &tls.Config{Certificates: []tls.Certificate{serverCert}, MinVersion: tls.VersionTLS12}

	tests := []struct {
		name      string
		tlsConfig *tls.Config
		password  string
		wantAuth  bool
		wantTLS   bool
	}{
		{name: "plain", wantAuth: false, wantTLS: false},
		{name: "password only", password: "hunter2", wantAuth: true, wantTLS: false},
		{name: "tls only", tlsConfig: clientTLS, wantAuth: false, wantTLS: true},
		{name: "tls and password", tlsConfig: clientTLS, password: "hunter2", wantAuth: true, wantTLS: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var srvTLS *tls.Config
			if tc.wantTLS {
				srvTLS = serverTLS
			}
			addr, captures := capturingValkeyServer(t, srvTLS)

			// No NewValkeyClientFn: exercise the production constructor selection.
			r := &ValkeyReconciler{}
			cl := r.newValkeyClient(addr, tc.password, tc.tlsConfig)
			require.NoError(t, cl.Ping(), "the fake server answers PING with +PONG")

			got := awaitCapture(t, captures)
			assert.Contains(t, got.data, "PING")
			if tc.wantAuth {
				assert.Contains(t, got.data, "AUTH", "a configured password must be sent as AUTH")
				assert.Contains(t, got.data, "hunter2")
				assert.Less(t, strings.Index(got.data, "AUTH"), strings.Index(got.data, "PING"),
					"AUTH must precede the command")
			} else {
				assert.NotContains(t, got.data, "AUTH", "no password means no AUTH on the wire")
			}
			if tc.wantTLS {
				assert.GreaterOrEqual(t, got.tlsVersion, uint16(tls.VersionTLS12),
					"the connection must be TLS 1.2 or newer")
			} else {
				assert.Zero(t, got.tlsVersion)
			}
		})
	}
}

func TestNewValkeyClient_PrefersInjectedFactory(t *testing.T) {
	addr, captures := capturingValkeyServer(t, nil)

	var gotAddr, gotPassword string
	r := &ValkeyReconciler{
		NewValkeyClientFn: func(a, p string, _ *tls.Config) *valkeyclient.Client {
			gotAddr, gotPassword = a, p
			// Deliberately drop the password: the factory owns the decision.
			return valkeyclient.New(addr)
		},
	}

	cl := r.newValkeyClient("valkey-0.example:6379", "hunter2", nil)
	require.NoError(t, cl.Ping())

	assert.Equal(t, "valkey-0.example:6379", gotAddr, "the factory must receive the requested address")
	assert.Equal(t, "hunter2", gotPassword)
	got := awaitCapture(t, captures)
	assert.NotContains(t, got.data, "AUTH",
		"the injected factory decides the client, so the built-in AUTH path must not run as well")
}

// --- buildTLSConfig ---

// tlsSecretName is the name of the user-provided TLS Secret used by the
// buildTLSConfig tests.
const tlsSecretName = "valkey-tls"

func tlsValkey() *vkov1.Valkey {
	return newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, SecretName: tlsSecretName}
	})
}

func caSecret(name string, caPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       map[string][]byte{"ca.crt": caPEM},
	}
}

func TestBuildTLSConfig_NilWhenTLSDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	cfg, err := r.buildTLSConfig(context.Background(), v, "test-tls")

	require.NoError(t, err)
	assert.Nil(t, cfg, "without TLS the client must not be handed a tls.Config")
}

// TestBuildTLSConfig_TrustsOnlyTheSecretCA is the security-relevant assertion:
// the returned config must verify the peer against exactly the CA from the
// Secret — not skip verification, and not fall back to the system roots.
func TestBuildTLSConfig_TrustsOnlyTheSecretCA(t *testing.T) {
	caPEM, serverCert := selfSignedCA(t, "trusted-ca")
	v := tlsValkey()
	r, _ := newTestReconciler(v, caSecret(tlsSecretName, caPEM))

	cfg, err := r.buildTLSConfig(context.Background(), v, tlsSecretName)

	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion, "TLS 1.0/1.1 must not be negotiable")
	assert.False(t, cfg.InsecureSkipVerify, "certificate verification must stay on")
	require.NotNil(t, cfg.RootCAs)

	// A bare reconciler, so the real client factory (not the unit-test redirect)
	// performs the handshake with the config under test.
	dialer := &ValkeyReconciler{}

	// A server presenting the trusted CA's certificate is accepted.
	trustedAddr, captures := capturingValkeyServer(t,
		&tls.Config{Certificates: []tls.Certificate{serverCert}, MinVersion: tls.VersionTLS12})
	require.NoError(t, dialer.newValkeyClient(trustedAddr, "", cfg).Ping(),
		"a peer signed by the Secret CA must be accepted")
	assert.GreaterOrEqual(t, awaitCapture(t, captures).tlsVersion, uint16(tls.VersionTLS12))

	// A server presenting an unrelated certificate is rejected.
	_, foreignCert := selfSignedCA(t, "untrusted-ca")
	foreignAddr, _ := capturingValkeyServer(t,
		&tls.Config{Certificates: []tls.Certificate{foreignCert}, MinVersion: tls.VersionTLS12})
	err = dialer.newValkeyClient(foreignAddr, "", cfg).Ping()
	require.Error(t, err, "a peer signed by an unknown CA must be rejected")
	assert.Contains(t, err.Error(), "certificate")
}

func TestBuildTLSConfig_ErrorWhenSecretMissing(t *testing.T) {
	v := tlsValkey()
	r, _ := newTestReconciler(v)

	cfg, err := r.buildTLSConfig(context.Background(), v, tlsSecretName)

	require.Error(t, err, "an unreadable CA must fail rather than silently disable verification")
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "reading TLS secret "+tlsSecretName)
}

func TestBuildTLSConfig_ErrorWhenCAKeyMissing(t *testing.T) {
	v := tlsValkey()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: tlsSecretName, Namespace: "default"},
		Data:       map[string][]byte{"tls.crt": []byte("irrelevant")},
	}
	r, _ := newTestReconciler(v, secret)

	cfg, err := r.buildTLSConfig(context.Background(), v, tlsSecretName)

	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "missing ca.crt")
}

func TestBuildTLSConfig_ErrorWhenCAIsNotPEM(t *testing.T) {
	v := tlsValkey()
	r, _ := newTestReconciler(v, caSecret(tlsSecretName, []byte("-----BEGIN CERTIFICATE-----\nnot base64\n")))

	cfg, err := r.buildTLSConfig(context.Background(), v, tlsSecretName)

	require.Error(t, err, "an unparseable CA must not yield an empty trust pool that verifies nothing")
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to parse CA certificate")
}

// --- readValkeyPassword / sentinelPassword ---

func authValkey(secretName, key string) *vkov1.Valkey {
	return newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: secretName, SecretPasswordKey: key}
	})
}

func TestReadValkeyPassword(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "valkey-auth", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("s3cr3t")},
	}

	t.Run("auth disabled", func(t *testing.T) {
		v := newTestValkey("test", "default")
		r, _ := newTestReconciler(v, secret)
		assert.Empty(t, r.readValkeyPassword(context.Background(), v))
	})

	t.Run("reads the configured key", func(t *testing.T) {
		v := authValkey("valkey-auth", "password")
		r, _ := newTestReconciler(v, secret)
		assert.Equal(t, "s3cr3t", r.readValkeyPassword(context.Background(), v))
	})

	t.Run("missing secret falls back to no password", func(t *testing.T) {
		v := authValkey("does-not-exist", "password")
		r, _ := newTestReconciler(v)
		assert.Empty(t, r.readValkeyPassword(context.Background(), v),
			"an unreadable auth Secret must not abort the reconcile; the connection is retried unauthenticated")
	})

	t.Run("wrong key yields no password", func(t *testing.T) {
		v := authValkey("valkey-auth", "not-the-key")
		r, _ := newTestReconciler(v, secret)
		assert.Empty(t, r.readValkeyPassword(context.Background(), v))
	})
}

// --- getInstanceChecker ---

func TestGetInstanceChecker_FallsBackToRealChecker(t *testing.T) {
	injected := &mockInstanceChecker{}
	r := &ValkeyReconciler{InstanceChecker: injected}
	assert.Same(t, injected, r.getInstanceChecker())

	bare, c := newTestReconciler()
	bare.InstanceChecker = nil
	checker := bare.getInstanceChecker()
	require.NotNil(t, checker, "a reconciler without an injected checker must build the real one")
	assert.IsType(t, health.NewChecker(c), checker)
}
