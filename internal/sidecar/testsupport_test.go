package sidecar

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeValkeyServer starts a RESP server on 127.0.0.1 that answers every command
// through handler and returns its address. It speaks the same wire protocol the
// production client writes, so tests that use it exercise valkeyclient for real
// instead of a mock echoing itself. Pass a non-nil tlsCfg to serve TLS.
func fakeValkeyServer(t *testing.T, tlsCfg *tls.Config, handler func(args []string) string) string {
	t.Helper()

	var ln net.Listener
	var err error
	if tlsCfg != nil {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			go serveFakeValkey(conn, handler)
		}
	}()

	return ln.Addr().String()
}

// serveFakeValkey answers every RESP command on one connection until the peer
// closes it. The production client opens a fresh connection per command, so the
// loop mainly covers an AUTH followed by the real command.
func serveFakeValkey(conn net.Conn, handler func(args []string) string) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPCommand(reader)
		if err != nil {
			return
		}
		if _, err := conn.Write([]byte(handler(args))); err != nil {
			return
		}
	}
}

// readRESPCommand reads one RESP array of bulk strings.
func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "*") {
		return nil, fmt.Errorf("unexpected command header %q", header)
	}
	count, err := strconv.Atoi(header[1:])
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if _, err := reader.ReadString('\n'); err != nil {
			return nil, err
		}
		data, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		args = append(args, strings.TrimRight(data, "\r\n"))
	}
	return args, nil
}

// respBulk renders a RESP bulk string reply.
func respBulk(payload string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(payload), payload)
}

// infoReplicationReply renders an INFO REPLICATION payload for the given role.
func infoReplicationReply(role string) string {
	return respBulk(fmt.Sprintf("# Replication\r\nrole:%s\r\nconnected_slaves:0\r\n", role))
}

// testCerts holds the paths of a generated self-signed certificate usable both
// as a CA bundle and as a client/server key pair.
type testCerts struct {
	caPath   string
	certPath string
	keyPath  string
	dir      string
}

// generateTestCerts writes a self-signed certificate for 127.0.0.1 into a temp
// directory and returns the paths. The same file serves as CA and leaf, which is
// all the TLS config builder and the fake TLS server need.
func generateTestCerts(t *testing.T) testCerts {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "valkey-sidecar-test"},
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

	dir := t.TempDir()
	certs := testCerts{
		dir:      dir,
		caPath:   filepath.Join(dir, "ca.crt"),
		certPath: filepath.Join(dir, "tls.crt"),
		keyPath:  filepath.Join(dir, "tls.key"),
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	require.NoError(t, os.WriteFile(certs.caPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(certs.certPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(certs.keyPath, keyPEM, 0o600))

	return certs
}

// serverTLSConfig builds a TLS server config from the generated certificate.
func (c testCerts) serverTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	require.NoError(t, err)
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
}
