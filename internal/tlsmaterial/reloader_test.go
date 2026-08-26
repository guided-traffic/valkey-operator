package tlsmaterial

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
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests below are the reason this package exists in the unit tier at all.
// The production failure needs a pod that outlives a 90-day certificate, which
// no e2e can reach and no clock injection makes cheap. Rewriting the files under
// a live TLS listener reproduces exactly the event that broke it -- kubelet
// replacing the mounted material while the process runs -- in milliseconds.

// --- material fixtures ---

// pair is a self-signed CA usable both as a trust anchor and as a leaf, which is
// all a loopback listener and a client certificate need.
type pair struct {
	certPEM []byte
	keyPEM  []byte
	serial  *big.Int
}

func newPair(t *testing.T, serial int64) pair {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "tlsmaterial-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	return pair{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		serial:  big.NewInt(serial),
	}
}

func (p pair) serverConfig(t *testing.T, clientCAs *x509.CertPool) *tls.Config {
	t.Helper()
	cert, err := tls.X509KeyPair(p.certPEM, p.keyPEM)
	require.NoError(t, err)
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if clientCAs != nil {
		cfg.ClientCAs = clientCAs
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg
}

func (p pair) pool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(p.certPEM))
	return pool
}

// mount writes a Secret-volume-shaped directory and returns the three paths.
type mount struct {
	dir      string
	caPath   string
	certPath string
	keyPath  string
}

func newMount(t *testing.T, ca, leaf pair) mount {
	t.Helper()
	dir := t.TempDir()
	m := mount{
		dir:      dir,
		caPath:   filepath.Join(dir, "ca.crt"),
		certPath: filepath.Join(dir, "tls.crt"),
		keyPath:  filepath.Join(dir, "tls.key"),
	}
	m.write(t, ca, leaf)
	return m
}

func (m mount) write(t *testing.T, ca, leaf pair) {
	t.Helper()
	require.NoError(t, os.WriteFile(m.caPath, ca.certPEM, 0o600))
	require.NoError(t, os.WriteFile(m.certPath, leaf.certPEM, 0o600))
	require.NoError(t, os.WriteFile(m.keyPath, leaf.keyPEM, 0o600))
}

// tlsEcho starts a TLS listener that records the client certificate of every
// accepted connection and closes it again. Only the handshake matters here.
func tlsEcho(t *testing.T, cfg *tls.Config) (addr string, clientSerials func() []string) {
	t.Helper()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var serials []string

	go func() {
		for {
			conn, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			tlsConn, ok := conn.(*tls.Conn)
			if !ok {
				_ = conn.Close()
				continue
			}
			if hErr := tlsConn.Handshake(); hErr == nil {
				mu.Lock()
				for _, peer := range tlsConn.ConnectionState().PeerCertificates {
					serials = append(serials, peer.SerialNumber.String())
				}
				mu.Unlock()
			}
			_ = conn.Close()
		}
	}()

	return ln.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), serials...)
	}
}

func dial(t *testing.T, addr string, cfg *tls.Config) error {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		return err
	}
	// The server closes right after the handshake; a read is what surfaces a
	// client certificate the server rejected, since that alert arrives after the
	// client considers the handshake done.
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	_, _ = conn.Read(buf)
	return conn.Close()
}

// --- the failure this package exists for ---

// A rotated client certificate reaches the server without the process
// restarting. This is the exact production failure inverted: the sidecar used to
// keep presenting the keypair it parsed at startup until the server rejected it
// as expired.
func TestReloader_PresentsARotatedClientCertificate(t *testing.T) {
	ca := newPair(t, 1)
	first := newPair(t, 100)
	second := newPair(t, 200)

	// The server trusts any of the three as a client, so the only thing that can
	// change the recorded serial is which certificate the client chose to send.
	clientCAs := first.pool(t)
	require.True(t, clientCAs.AppendCertsFromPEM(second.certPEM))
	addr, serials := tlsEcho(t, ca.serverConfig(t, clientCAs))

	m := newMount(t, ca, first)
	r, err := New(m.caPath, m.certPath, m.keyPath)
	require.NoError(t, err)

	require.NoError(t, dial(t, addr, r.Config()))
	require.Equal(t, []string{"100"}, serials())

	// cert-manager rotates: the same mount now holds a different keypair.
	m.write(t, ca, second)

	require.NoError(t, dial(t, addr, r.Config()))
	assert.Equal(t, []string{"100", "200"}, serials(),
		"the second handshake must present the material that is on disk now")
}

// A rotated CA is picked up too. RootCAs cannot be swapped per handshake through
// a callback the way a client certificate can, which is why the reloader hands
// back a rebuilt config instead of mutating one -- and why an issuer rotation
// would otherwise have the identical silent failure shape.
func TestReloader_TrustsARotatedCA(t *testing.T) {
	oldCA := newPair(t, 1)
	newCA := newPair(t, 2)
	client := newPair(t, 100)

	addr, _ := tlsEcho(t, newCA.serverConfig(t, nil))

	m := newMount(t, oldCA, client)
	r, err := New(m.caPath, m.certPath, m.keyPath)
	require.NoError(t, err)

	require.Error(t, dial(t, addr, r.Config()),
		"the server certificate is signed by a CA the mount does not carry yet")

	m.write(t, newCA, client)

	assert.NoError(t, dial(t, addr, r.Config()))
}

// --- caching and failure handling ---

func TestReloader_UnchangedFilesAreNotReparsed(t *testing.T) {
	ca := newPair(t, 1)
	m := newMount(t, ca, ca)

	r, err := New(m.caPath, m.certPath, m.keyPath)
	require.NoError(t, err)

	first := r.Config()
	assert.Same(t, first, r.Config(), "identical bytes must yield the very same config")

	m.write(t, ca, newPair(t, 300))
	assert.NotSame(t, first, r.Config(), "changed bytes must yield a rebuilt config")
}

// kubelet swaps the ..data symlink, so a caller can read tls.crt from one
// revision and tls.key from the next. That pair does not match, X509KeyPair
// rejects it, and the answer is the last config that worked -- not an error that
// would take the labeler down for the length of one write.
func TestReloader_KeepsTheLastGoodConfigOnAMismatchedPair(t *testing.T) {
	ca := newPair(t, 1)
	first := newPair(t, 100)
	second := newPair(t, 200)
	m := newMount(t, ca, first)

	r, err := New(m.caPath, m.certPath, m.keyPath)
	require.NoError(t, err)
	good := r.Config()

	// Only the certificate half of the rotation has landed.
	require.NoError(t, os.WriteFile(m.certPath, second.certPEM, 0o600))
	assert.Same(t, good, r.Config(), "a half-written mount must not replace a working config")

	// The key half lands; the pair matches again and is adopted.
	require.NoError(t, os.WriteFile(m.keyPath, second.keyPEM, 0o600))
	assert.NotSame(t, good, r.Config())
	assert.Equal(t, second.serial, r.Config().Certificates[0].Leaf.SerialNumber)
}

func TestReloader_KeepsTheLastGoodConfigWhenAFileDisappears(t *testing.T) {
	ca := newPair(t, 1)
	m := newMount(t, ca, ca)

	r, err := New(m.caPath, m.certPath, m.keyPath)
	require.NoError(t, err)
	good := r.Config()

	require.NoError(t, os.Remove(m.caPath))
	assert.Same(t, good, r.Config())

	// And it recovers rather than staying degraded once the file is back.
	m.write(t, ca, ca)
	assert.NotNil(t, r.Config().RootCAs)
}

func TestReloader_UnparseableCAIsKeptOut(t *testing.T) {
	ca := newPair(t, 1)
	m := newMount(t, ca, ca)

	r, err := New(m.caPath, m.certPath, m.keyPath)
	require.NoError(t, err)
	good := r.Config()

	require.NoError(t, os.WriteFile(m.caPath, []byte("not a PEM block"), 0o600))
	assert.Same(t, good, r.Config())
}

// --- construction ---

func TestNew_FailsOnMaterialThatCannotBeUsed(t *testing.T) {
	ca := newPair(t, 1)
	m := newMount(t, ca, ca)

	t.Run("absent file", func(t *testing.T) {
		_, err := New(filepath.Join(m.dir, "absent.crt"), "", "")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading TLS material")
	})

	t.Run("CA that is not a certificate", func(t *testing.T) {
		broken := filepath.Join(m.dir, "broken-ca.crt")
		require.NoError(t, os.WriteFile(broken, []byte("nope"), 0o600))

		_, err := New(broken, "", "")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse CA certificate")
	})

	t.Run("key that does not match the certificate", func(t *testing.T) {
		other := newPair(t, 999)
		mismatched := filepath.Join(m.dir, "other.key")
		require.NoError(t, os.WriteFile(mismatched, other.keyPEM, 0o600))

		_, err := New(m.caPath, m.certPath, mismatched)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "loading client certificate")
	})
}

func TestNew_NoFilesYieldsAMinimumVersionOnly(t *testing.T) {
	r, err := New("", "", "")

	require.NoError(t, err)
	cfg := r.Config()
	assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
	assert.Nil(t, cfg.RootCAs, "an empty CA path leaves verification to the system roots")
	assert.Empty(t, cfg.Certificates)
}

// The CA-only shape the observer runs in by default, and the one the operator's
// reconciler and health checker have always used.
func TestNew_CAOnlyPresentsNoClientCertificate(t *testing.T) {
	ca := newPair(t, 1)
	m := newMount(t, ca, ca)

	r, err := New(m.caPath, "", "")

	require.NoError(t, err)
	assert.NotNil(t, r.Config().RootCAs)
	assert.Empty(t, r.Config().Certificates)
}

// The doc comment claims the Reloader is safe for concurrent use; this is what
// keeps that claim honest under -race.
func TestReloader_ConcurrentConfigIsSerialised(t *testing.T) {
	ca := newPair(t, 1)
	m := newMount(t, ca, ca)

	r, err := New(m.caPath, m.certPath, m.keyPath)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NotNil(t, r.Config())
		}()
	}
	wg.Wait()
}
