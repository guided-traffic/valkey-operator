package health

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// servedCertPair is a self-signed certificate usable as CA and leaf at once,
// which is all a loopback listener needs.
type servedCertPair struct {
	certPEM []byte
	keyPEM  []byte
	cert    *x509.Certificate
}

func newServedCertPair(t *testing.T, serial int64) servedCertPair {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "served-cert-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return servedCertPair{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		cert:    parsed,
	}
}

func secretWithLeaf(p servedCertPair) *corev1.Secret {
	return &corev1.Secret{Data: map[string][]byte{builder.TLSCertKey: p.certPEM}}
}

// The core promise: a mismatch between served and mounted material is a report,
// never a handshake failure -- an observation must not be able to turn into an
// outage (ADR 0030 D4). Proven against a real handshake: the server presents
// certificate A, the Secret holds certificate B, and the dial still succeeds.
func TestObserveServedCertificate_AMismatchNeverFailsTheHandshake(t *testing.T) {
	serving := newServedCertPair(t, 1)
	inSecret := newServedCertPair(t, 2)

	serverCert, err := tls.X509KeyPair(serving.certPEM, serving.keyPEM)
	require.NoError(t, err)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{serverCert}, MinVersion: tls.VersionTLS12,
	})
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			_ = conn.Close()
		}
	}()

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(serving.certPEM))
	cfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	observeServedCertificate(logr.Discard(), cfg, secretWithLeaf(inSecret))
	require.NotNil(t, cfg.VerifyConnection, "the hook must be armed when the Secret carries a leaf")

	conn, err := tls.Dial("tcp", ln.Addr().String(), cfg)
	require.NoError(t, err, "the mismatch is reported, never enforced")
	_ = conn.Close()
}

func TestObserveServedCertificate_SkipsWithoutALeafInTheSecret(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	observeServedCertificate(logr.Discard(), cfg, &corev1.Secret{Data: map[string][]byte{}})
	assert.Nil(t, cfg.VerifyConnection)

	observeServedCertificate(logr.Discard(), cfg,
		&corev1.Secret{Data: map[string][]byte{builder.TLSCertKey: []byte("not a pem block")}})
	assert.Nil(t, cfg.VerifyConnection, "an unparsable leaf arms nothing rather than failing")
}

// A resumed session or an exotic peer can present no certificate; the hook
// stays inert instead of dereferencing an empty list.
func TestObserveServedCertificate_ToleratesAnEmptyPeerList(t *testing.T) {
	p := newServedCertPair(t, 3)
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	observeServedCertificate(logr.Discard(), cfg, secretWithLeaf(p))
	require.NotNil(t, cfg.VerifyConnection)

	assert.NoError(t, cfg.VerifyConnection(tls.ConnectionState{}))
	assert.NoError(t, cfg.VerifyConnection(tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{p.cert},
	}), "the matching case reports nothing and fails nothing")
}
