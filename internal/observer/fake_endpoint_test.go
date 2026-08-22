package observer

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
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The observer talks to Valkey and Sentinel over a raw RESP socket through
// internal/valkeyclient, which offers no seam for a mock. The helpers in this
// file therefore provide the real thing at the socket level: a listener that
// parses RESP requests, answers them, and records what it was asked. Tests can
// then assert on the commands the observer actually sent instead of on a stub
// handing back what it was told to hand back.

// Command names shared by the fake endpoints and the assertions.
const (
	cmdSet = "SET"
	cmdGet = "GET"
)

// fakeRESP is a RESP endpoint on loopback driven by a per-command handler.
type fakeRESP struct {
	addr string
	port string

	mu       sync.Mutex
	received [][]string
}

// startFakeRESP starts a plaintext listener that answers every RESP command with
// the string returned by handler. An empty return value makes the endpoint close
// the connection without answering, which simulates a hung or crashed server.
func startFakeRESP(t *testing.T, handler func(args []string) string) *fakeRESP {
	t.Helper()
	return startFakeEndpoint(t, handler, nil)
}

// startFakeRESPTLS starts the same endpoint behind a TLS-only listener using the
// key pair from writeTestCerts. A plaintext client never gets past the
// handshake, so reaching the RESP layer proves the client really negotiated TLS.
func startFakeRESPTLS(t *testing.T, handler func(args []string) string, certPath, keyPath string) *fakeRESP {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)
	return startFakeEndpoint(t, handler, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
}

// startFakeEndpoint binds a loopback listener and serves RESP on it, optionally
// wrapping every accepted connection in TLS.
func startFakeEndpoint(t *testing.T, handler func(args []string) string, srvTLS *tls.Config) *fakeRESP {
	t.Helper()

	// Bind to "localhost" so that the address family matches what a client
	// dialling the hostname "localhost" resolves to first.
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	f := &fakeRESP{addr: ln.Addr().String(), port: port}
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			if srvTLS != nil {
				// tls.Server handshakes lazily on the first Read, so a
				// plaintext client simply fails there and is dropped.
				conn = tls.Server(conn, srvTLS)
			}
			go f.serve(conn, handler)
		}
	}()
	return f
}

func (f *fakeRESP) serve(conn net.Conn, handler func(args []string) string) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPCommand(reader)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.received = append(f.received, args)
		f.mu.Unlock()

		reply := handler(args)
		if reply == "" {
			return
		}
		if _, wErr := conn.Write([]byte(reply)); wErr != nil {
			return
		}
	}
}

// commands returns every command the endpoint received, in order.
func (f *fakeRESP) commands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.received))
	copy(out, f.received)
	return out
}

// sawCommand reports whether the endpoint received a command whose first words
// match the given prefix, e.g. sawCommand("SENTINEL", "MASTER").
func (f *fakeRESP) sawCommand(prefix ...string) bool {
	return f.findCommand(prefix...) != nil
}

// findCommand returns the first received command matching the prefix, nil if none.
func (f *fakeRESP) findCommand(prefix ...string) []string {
	for _, cmd := range f.commands() {
		if len(cmd) < len(prefix) {
			continue
		}
		match := true
		for i, want := range prefix {
			if !strings.EqualFold(cmd[i], want) {
				match = false
				break
			}
		}
		if match {
			return cmd
		}
	}
	return nil
}

// readRESPCommand parses one RESP array request ("*N $len arg ...").
func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("not a RESP array: %q", line)
	}
	count, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, fmt.Errorf("bad array header %q: %w", line, err)
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		header, hErr := r.ReadString('\n')
		if hErr != nil {
			return nil, hErr
		}
		header = strings.TrimRight(header, "\r\n")
		if !strings.HasPrefix(header, "$") {
			return nil, fmt.Errorf("not a bulk string: %q", header)
		}
		size, sErr := strconv.Atoi(header[1:])
		if sErr != nil {
			return nil, fmt.Errorf("bad bulk header %q: %w", header, sErr)
		}
		buf := make([]byte, size+2)
		if _, rErr := io.ReadFull(r, buf); rErr != nil {
			return nil, rErr
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

// --- RESP reply builders ---

func respSimple(s string) string { return "+" + s + "\r\n" }

func respError(msg string) string { return "-ERR " + msg + "\r\n" }

func respBulk(s string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }

func respNil() string { return "$-1\r\n" }

func respArray(items ...string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d\r\n", len(items))
	for _, item := range items {
		sb.WriteString(respBulk(item))
	}
	return sb.String()
}

func respArrayOfArrays(groups ...[]string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d\r\n", len(groups))
	for _, group := range groups {
		sb.WriteString(respArray(group...))
	}
	return sb.String()
}

// --- Fake Valkey data node ---

// fakeValkeyNode answers the commands the observer sends to a data pod and
// keeps the health key it was told to store, so a passing read check proves the
// observer read back exactly the value it wrote.
type fakeValkeyNode struct {
	mu    sync.Mutex
	store map[string]string

	role            string
	connectedSlaves int
	syncInProgress  bool

	// Failure injection. Each holds a raw RESP reply that replaces the normal one.
	pingReply string
	setReply  string
	infoReply string

	// getOverride replaces the stored value on GET when non-nil.
	getOverride *string

	// selectedDB records the argument of the last SELECT.
	selectedDB string
}

func newFakeValkeyNode() *fakeValkeyNode {
	return &fakeValkeyNode{store: make(map[string]string), role: roleMaster}
}

func (n *fakeValkeyNode) handle(args []string) string {
	if len(args) == 0 {
		return respError("empty command")
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	switch strings.ToUpper(args[0]) {
	case "AUTH":
		return respSimple("OK")
	case "PING":
		if n.pingReply != "" {
			return n.pingReply
		}
		return respSimple("PONG")
	case selectCommand:
		n.selectedDB = args[1]
		return respSimple("OK")
	case cmdSet:
		if n.setReply != "" {
			return n.setReply
		}
		n.store[args[1]] = args[2]
		return respSimple("OK")
	case cmdGet:
		if n.getOverride != nil {
			return respBulk(*n.getOverride)
		}
		val, ok := n.store[args[1]]
		if !ok {
			return respNil()
		}
		return respBulk(val)
	case "INFO":
		if n.infoReply != "" {
			return n.infoReply
		}
		return respBulk(n.replicationInfo())
	default:
		return respSimple("OK")
	}
}

func (n *fakeValkeyNode) replicationInfo() string {
	sync := "0"
	if n.syncInProgress {
		sync = "1"
	}
	return fmt.Sprintf("# Replication\r\nrole:%s\r\nconnected_slaves:%d\r\nmaster_sync_in_progress:%s\r\n",
		n.role, n.connectedSlaves, sync)
}

// configure mutates the node under its own lock so a test can inject a failure
// after the endpoint is already serving. fn must not call another method of the
// node.
func (n *fakeValkeyNode) configure(fn func(n *fakeValkeyNode)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	fn(n)
}

func (n *fakeValkeyNode) storedHealthValue() (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	val, ok := n.store[observerHealthKey]
	return val, ok
}

func (n *fakeValkeyNode) lastSelectedDB() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.selectedDB
}

// --- Fake Sentinel node ---

// fakeSentinelNode answers SENTINEL MASTER / SENTINEL REPLICAS and records
// whether the observer authenticated.
type fakeSentinelNode struct {
	mu sync.Mutex

	masterIP   string
	masterPort string
	flags      string
	replicas   [][2]string

	// Failure injection: raw RESP replies replacing the normal ones.
	pingReply         string
	masterReply       string
	replicasReply     string
	sawAuth           bool
	authenticatedWith string
}

func newFakeSentinelNode(masterIP, masterPort string) *fakeSentinelNode {
	return &fakeSentinelNode{
		masterIP:   masterIP,
		masterPort: masterPort,
		flags:      "master",
		replicas:   [][2]string{{"valkey-1.example.invalid", "6379"}},
	}
}

func (s *fakeSentinelNode) handle(args []string) string {
	if len(args) == 0 {
		return respError("empty command")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	switch strings.ToUpper(args[0]) {
	case "AUTH":
		s.sawAuth = true
		s.authenticatedWith = args[1]
		return respSimple("OK")
	case "PING":
		if s.pingReply != "" {
			return s.pingReply
		}
		return respSimple("PONG")
	case "SENTINEL":
		return s.handleSentinel(args)
	default:
		return respSimple("OK")
	}
}

func (s *fakeSentinelNode) handleSentinel(args []string) string {
	if len(args) < 2 {
		return respError("wrong number of arguments")
	}
	switch strings.ToUpper(args[1]) {
	case "MASTER":
		if s.masterReply != "" {
			return s.masterReply
		}
		return respArray(
			"name", "mymaster",
			"ip", s.masterIP,
			"port", s.masterPort,
			"flags", s.flags,
			"num-slaves", strconv.Itoa(len(s.replicas)),
			"quorum", "2",
		)
	case "REPLICAS":
		if s.replicasReply != "" {
			return s.replicasReply
		}
		groups := make([][]string, 0, len(s.replicas))
		for _, r := range s.replicas {
			groups = append(groups, []string{"ip", r[0], "port", r[1], "flags", "slave"})
		}
		return respArrayOfArrays(groups...)
	default:
		return respSimple("OK")
	}
}

// configure mutates the sentinel under its own lock. fn must not call another
// method of the sentinel.
func (s *fakeSentinelNode) configure(fn func(s *fakeSentinelNode)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func (s *fakeSentinelNode) authObserved() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawAuth, s.authenticatedWith
}

// --- Misc helpers ---

// closedAddr returns a loopback address with nothing listening on it. Dialling it
// is refused instantly, unlike an unresolvable hostname which costs a DNS timeout.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// closedPort returns the port half of an address nobody is listening on.
func closedPort(t *testing.T) string {
	t.Helper()
	_, port, err := net.SplitHostPort(closedAddr(t))
	require.NoError(t, err)
	return port
}

// writeTestCerts generates a self-signed certificate usable as a CA bundle, as a
// client key pair, and as the server certificate of a loopback listener, and
// returns the three file paths. Every call produces a fresh key, so two calls
// yield two mutually untrusted CAs.
func writeTestCerts(t *testing.T) (caPath, certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "vko-observer-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		// The loopback SANs let the same certificate serve startFakeRESPTLS:
		// tls.Dial verifies against the host half of the dialled address, which
		// for a loopback listener is an IP literal.
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	dir := t.TempDir()
	caPath = filepath.Join(dir, "ca.crt")
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	require.NoError(t, os.WriteFile(caPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(certPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))
	return caPath, certPath, keyPath
}
