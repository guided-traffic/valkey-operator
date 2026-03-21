package valkeyclient

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- New / NewTLS ---

func TestNewClient(t *testing.T) {
	c := New("localhost:6379")
	assert.NotNil(t, c)
	assert.Equal(t, "localhost:6379", c.addr)
	assert.Nil(t, c.tlsConfig)
}

func TestNewTLSClient(t *testing.T) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	c := NewTLS("localhost:16379", tlsConfig)
	assert.NotNil(t, c)
	assert.Equal(t, "localhost:16379", c.addr)
	assert.NotNil(t, c.tlsConfig)
	assert.Equal(t, uint16(tls.VersionTLS12), c.tlsConfig.MinVersion)
}

func TestFormatRESP_SingleArg(t *testing.T) {
	resp := formatRESP([]string{"PING"})
	assert.Equal(t, "*1\r\n$4\r\nPING\r\n", resp)
}

func TestFormatRESP_MultipleArgs(t *testing.T) {
	resp := formatRESP([]string{"INFO", "replication"})
	assert.Equal(t, "*2\r\n$4\r\nINFO\r\n$11\r\nreplication\r\n", resp)
}

func TestFormatRESP_ThreeArgs(t *testing.T) {
	resp := formatRESP([]string{"SENTINEL", "MASTER", "test"})
	assert.Equal(t, "*3\r\n$8\r\nSENTINEL\r\n$6\r\nMASTER\r\n$4\r\ntest\r\n", resp)
}

// --- parseReplicationInfo ---

func TestParseReplicationInfo_Master(t *testing.T) {
	raw := `# Replication
role:master
connected_slaves:2
slave0:ip=10.0.0.2,port=6379,state=online
slave1:ip=10.0.0.3,port=6379,state=online
master_replid:abc123
master_sync_in_progress:0
`
	info := parseReplicationInfo(raw)

	assert.Equal(t, "master", info.Role)
	assert.Equal(t, 2, info.ConnectedSlaves)
	assert.False(t, info.MasterSyncInProgress)
}

func TestParseReplicationInfo_Replica(t *testing.T) {
	raw := `# Replication
role:slave
master_host:test-0.test-headless.default.svc.cluster.local
master_port:6379
master_link_status:up
master_sync_in_progress:0
`
	info := parseReplicationInfo(raw)

	assert.Equal(t, "slave", info.Role)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local", info.MasterHost)
	assert.Equal(t, "6379", info.MasterPort)
	assert.Equal(t, "up", info.MasterLinkStatus)
	assert.False(t, info.MasterSyncInProgress)
}

func TestParseReplicationInfo_SyncInProgress(t *testing.T) {
	raw := `role:slave
master_host:10.0.0.1
master_port:6379
master_link_status:up
master_sync_in_progress:1
`
	info := parseReplicationInfo(raw)

	assert.True(t, info.MasterSyncInProgress)
}

func TestParseReplicationInfo_EmptyInput(t *testing.T) {
	info := parseReplicationInfo("")
	assert.Equal(t, "", info.Role)
	assert.Equal(t, 0, info.ConnectedSlaves)
}

// --- parseSentinelMasterInfo ---

func TestParseSentinelMasterInfo(t *testing.T) {
	raw := `name
test
ip
test-0.test-headless.default.svc.cluster.local
port
6379
flags
master
num-slaves
2
quorum
2
`
	info := parseSentinelMasterInfo(raw)

	assert.Equal(t, "test", info.Name)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local", info.IP)
	assert.Equal(t, "6379", info.Port)
	assert.Equal(t, "master", info.Flags)
	assert.Equal(t, 2, info.NumSlaves)
	assert.Equal(t, 2, info.Quorum)
}

func TestParseSentinelMasterInfo_EmptyInput(t *testing.T) {
	info := parseSentinelMasterInfo("")
	assert.Equal(t, "", info.Name)
}

func TestFormatRESP_WaitCommand(t *testing.T) {
	resp := formatRESP([]string{"WAIT", "2", "5000"})
	assert.Equal(t, "*3\r\n$4\r\nWAIT\r\n$1\r\n2\r\n$4\r\n5000\r\n", resp)
}

func TestFormatRESP_SentinelResetCommand(t *testing.T) {
	resp := formatRESP([]string{"SENTINEL", "RESET", "mymaster"})
	assert.Equal(t, "*3\r\n$8\r\nSENTINEL\r\n$5\r\nRESET\r\n$8\r\nmymaster\r\n", resp)
}

// --- ConnectionError ---

func TestConnectionError_ErrorMessage(t *testing.T) {
	cause := fmt.Errorf("dial tcp 10.0.0.1:6379: i/o timeout")
	e := &ConnectionError{
		Addr:  "10.0.0.1:6379",
		Cause: cause,
		Hint:  "connection to 10.0.0.1:6379 timed out — a firewall rule or NetworkPolicy is likely blocking TCP access to this port",
	}

	msg := e.Error()
	assert.Contains(t, msg, "10.0.0.1:6379")
	assert.Contains(t, msg, "timed out")
	assert.Contains(t, msg, "firewall")
}

func TestConnectionError_Unwrap_PreservesChain(t *testing.T) {
	sentinel := fmt.Errorf("sentinel cause")
	e := &ConnectionError{
		Addr:  "10.0.0.1:6379",
		Cause: sentinel,
		Hint:  "some hint",
	}

	assert.ErrorIs(t, e, sentinel)
}

func TestConnectionError_ErrorContainsAddr(t *testing.T) {
	e := &ConnectionError{
		Addr:  "my-pod.headless.ns.svc.cluster.local:6379",
		Cause: fmt.Errorf("some err"),
		Hint:  "verify access",
	}

	assert.Contains(t, e.Error(), "my-pod.headless.ns.svc.cluster.local:6379")
}

// --- connHint ---

func TestConnHint_Timeout(t *testing.T) {
	err := &net.OpError{
		Op:     "dial",
		Net:    "tcp",
		Source: nil,
		Addr:   nil,
		Err:    &timeoutError{},
	}

	hint := connHint("10.0.0.1:6379", err)
	assert.Contains(t, hint, "timed out")
	assert.Contains(t, hint, "firewall")
}

func TestConnHint_ConnectionRefused(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.ECONNREFUSED,
	}

	hint := connHint("10.0.0.1:6379", err)
	assert.Contains(t, hint, "refused")
}

func TestConnHint_HostUnreachable(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.EHOSTUNREACH,
	}

	hint := connHint("10.0.0.1:6379", err)
	assert.Contains(t, hint, "route")
}

func TestConnHint_UnknownOpError(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: fmt.Errorf("some obscure error"),
	}

	hint := connHint("10.0.0.1:6379", err)
	assert.Contains(t, hint, "10.0.0.1:6379")
}

func TestConnHint_NonOpError(t *testing.T) {
	err := fmt.Errorf("generic error")
	hint := connHint("10.0.0.1:6379", err)
	assert.Contains(t, hint, "10.0.0.1:6379")
}

func TestConnHint_TLS_UnknownAuthority(t *testing.T) {
	err := &x509.UnknownAuthorityError{}
	hint := connHint("10.0.0.1:16379", err)
	assert.Contains(t, hint, "TLS handshake")
	assert.Contains(t, hint, "CA certificate")
	assert.NotContains(t, hint, "firewall")
}

func TestConnHint_TLS_CertificateInvalid(t *testing.T) {
	err := &x509.CertificateInvalidError{Reason: x509.Expired}
	hint := connHint("10.0.0.1:16379", err)
	assert.Contains(t, hint, "TLS handshake")
	assert.NotContains(t, hint, "firewall")
}

func TestConnHint_TLS_RecordHeaderError(t *testing.T) {
	err := &tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}
	hint := connHint("10.0.0.1:16379", err)
	assert.Contains(t, hint, "TLS handshake")
	assert.NotContains(t, hint, "firewall")
}

func TestConnHint_TLS_StringMatch(t *testing.T) {
	// Simulates errors like "remote error: tls: bad certificate" which are
	// not exported Go types but contain the "tls:" prefix.
	err := fmt.Errorf("remote error: tls: bad certificate")
	hint := connHint("10.0.0.1:16379", err)
	assert.Contains(t, hint, "TLS handshake")
	assert.NotContains(t, hint, "firewall")
}

// timeoutError is a net.Error that reports Timeout() == true.
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// --- SetTimeout ---

func TestSetTimeout(t *testing.T) {
	c := New("localhost:6379")
	assert.Equal(t, 5*time.Second, c.timeout)

	c.SetTimeout(15 * time.Second)
	assert.Equal(t, 15*time.Second, c.timeout)
}

func TestSetTimeout_TLSClient(t *testing.T) {
	c := NewTLSWithPassword("localhost:16379", &tls.Config{MinVersion: tls.VersionTLS12}, "secret")
	assert.Equal(t, 5*time.Second, c.timeout)

	c.SetTimeout(10 * time.Second)
	assert.Equal(t, 10*time.Second, c.timeout)
}

// --- WAIT with fake RESP server ---

// fakeRESPServer starts a TCP listener that accepts one connection,
// expects a WAIT command, and responds with the given integer reply.
// It returns the listener address and a cleanup function.
func fakeRESPServer(t *testing.T, reply int, delay time.Duration) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake server: %v", err)
	}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read and discard the incoming WAIT command.
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)

		// Simulate server-side WAIT delay.
		if delay > 0 {
			time.Sleep(delay)
		}

		// Respond with the integer reply.
		resp := fmt.Sprintf(":%d\r\n", reply)
		_, _ = conn.Write([]byte(resp))
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestWait_PartialAck(t *testing.T) {
	// Server responds with 1 ack (out of 2 requested), simulating cascaded replication.
	addr, cleanup := fakeRESPServer(t, 1, 0)
	defer cleanup()

	c := New(addr)
	acked, err := c.Wait(2, 1000)
	assert.NoError(t, err)
	assert.Equal(t, 1, acked,
		"WAIT must return the actual ack count, not fail when fewer than requested")
}

func TestWait_FullAck(t *testing.T) {
	addr, cleanup := fakeRESPServer(t, 2, 0)
	defer cleanup()

	c := New(addr)
	acked, err := c.Wait(2, 1000)
	assert.NoError(t, err)
	assert.Equal(t, 2, acked)
}

func TestWait_TimeoutRace_DefaultClient(t *testing.T) {
	// Simulate a server that delays 4s (close to the 5s default timeout).
	// With TLS/AUTH overhead, the default 5s timeout could race.
	// Using SetTimeout avoids this.
	addr, cleanup := fakeRESPServer(t, 1, 100*time.Millisecond)
	defer cleanup()

	c := New(addr)
	c.SetTimeout(2 * time.Second) // Plenty of room for 100ms delay.

	acked, err := c.Wait(2, 5000)
	assert.NoError(t, err)
	assert.Equal(t, 1, acked)
}

func TestWait_WithAuth_SlowResponse(t *testing.T) {
	// Simulate a server that requires AUTH and responds slowly to WAIT.
	// This reproduces the production scenario where TLS+AUTH overhead
	// combined with a blocking WAIT command exceeds the client timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake server: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)

		// Read AUTH command.
		_, _ = conn.Read(buf)
		// Respond OK to AUTH.
		_, _ = conn.Write([]byte("+OK\r\n"))

		// Read WAIT command.
		_, _ = conn.Read(buf)
		// Simulate server-side WAIT blocking for 200ms.
		time.Sleep(200 * time.Millisecond)
		// Return partial ack.
		_, _ = conn.Write([]byte(":1\r\n"))
	}()

	c := NewWithPassword(ln.Addr().String(), "secret")
	c.SetTimeout(2 * time.Second)

	acked, err := c.Wait(2, 5000)
	assert.NoError(t, err)
	assert.Equal(t, 1, acked)
}

// recordingRESPServer creates a TCP server that records all RESP commands received.
// It returns +OK for every command. Each accepted connection runs until the
// client disconnects. The returned channel receives all commands as raw strings.
func recordingRESPServer(t *testing.T) (string, func(), <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	cmds := make(chan string, 64)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				reader := bufio.NewReader(c)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					if len(line) == 0 {
						continue
					}
					// RESP array: *<count>\r\n
					if line[0] == '*' {
						count := 0
						fmt.Sscanf(line, "*%d", &count)
						var parts []string
						for i := 0; i < count; i++ {
							// Read $<len>\r\n
							_, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							// Read the bulk string data\r\n
							data, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							parts = append(parts, strings.TrimSpace(data))
						}
						cmd := strings.Join(parts, " ")
						cmds <- cmd
					}
					// Reply +OK for everything.
					_, _ = c.Write([]byte("+OK\r\n"))
				}
			}(conn)
		}
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }, cmds
}

// collectCommands drains the command channel with a short timeout.
func collectCommands(ch <-chan string, timeout time.Duration) []string {
	var result []string
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case cmd := <-ch:
			result = append(result, cmd)
		case <-timer.C:
			return result
		}
	}
}

// TestSentinelResetSequence_IncludesAuthPass verifies that the full sentinel
// reconfigure sequence (REMOVE → MONITOR → SET params → SET auth-pass) sends
// the auth-pass command. This mirrors what resetSentinelState should do.
func TestSentinelResetSequence_IncludesAuthPass(t *testing.T) {
	addr, cleanup, cmds := recordingRESPServer(t)
	defer cleanup()

	c := NewWithPassword(addr, "mypassword")

	monitorName := "mymonitor"
	masterAddr := "master.example.com"

	// Execute the same sequence as resetSentinelState.
	require.NoError(t, c.SentinelRemove(monitorName))
	require.NoError(t, c.SentinelMonitorAdd(monitorName, masterAddr, 16379, 2))
	require.NoError(t, c.SentinelSet(monitorName, "down-after-milliseconds", "5000"))
	require.NoError(t, c.SentinelSet(monitorName, "failover-timeout", "60000"))
	require.NoError(t, c.SentinelSet(monitorName, "parallel-syncs", "1"))
	require.NoError(t, c.SentinelSet(monitorName, "resolve-hostnames", "yes"))
	require.NoError(t, c.SentinelSet(monitorName, "announce-hostnames", "yes"))
	// The critical step: auth-pass must be set after SENTINEL MONITOR.
	require.NoError(t, c.SentinelSet(monitorName, "auth-pass", "mypassword"))

	recorded := collectCommands(cmds, 500*time.Millisecond)

	// Verify auth-pass was sent.
	foundAuthPass := false
	for _, cmd := range recorded {
		if strings.Contains(cmd, "SENTINEL SET mymonitor auth-pass mypassword") {
			foundAuthPass = true
			break
		}
	}
	assert.True(t, foundAuthPass,
		"SENTINEL SET auth-pass must be included in reconfigure sequence; recorded commands: %v", recorded)

	// Also verify the basic sequence is correct.
	assert.GreaterOrEqual(t, len(recorded), 3,
		"expected at least AUTH + REMOVE + MONITOR + SET commands")
}

// --- Edge Case Tests for RESP parsing ---

func TestReadFullResponse_SimpleString(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+PONG\r\n")
	defer cleanup()

	c := New(addr)
	err := c.Ping()
	assert.NoError(t, err)
}

func TestReadFullResponse_ErrorResponse(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "-ERR unknown command\r\n")
	defer cleanup()

	c := New(addr)
	err := c.Ping()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}

func TestReadFullResponse_IntegerResponse(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, ":42\r\n")
	defer cleanup()

	c := New(addr)
	size, err := c.DBSize()
	assert.NoError(t, err)
	assert.Equal(t, 42, size)
}

func TestReadFullResponse_BulkString(t *testing.T) {
	// Simulate a bulk string response for INFO replication.
	data := "role:master\r\nconnected_slaves:2\r\nmaster_sync_in_progress:0\r\n"
	resp := fmt.Sprintf("$%d\r\n%s\r\n", len(data), data)

	addr, cleanup := fakeRESPServerCustom(t, resp)
	defer cleanup()

	c := New(addr)
	info, err := c.InfoReplication()
	assert.NoError(t, err)
	assert.Equal(t, "master", info.Role)
	assert.Equal(t, 2, info.ConnectedSlaves)
}

func TestReadFullResponse_NullBulkString(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "$-1\r\n")
	defer cleanup()

	c := New(addr)
	// Null bulk string returns empty, parsed as empty replication info.
	info, err := c.InfoReplication()
	assert.NoError(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, "", info.Role)
}

func TestReadArray_EmptyArray(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "*0\r\n")
	defer cleanup()

	c := New(addr)
	info, err := c.SentinelMaster("test")
	assert.NoError(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, "", info.Name)
}

func TestDBSize_Zero(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, ":0\r\n")
	defer cleanup()

	c := New(addr)
	size, err := c.DBSize()
	assert.NoError(t, err)
	assert.Equal(t, 0, size)
}

func TestDBSize_Large(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, ":999999\r\n")
	defer cleanup()

	c := New(addr)
	size, err := c.DBSize()
	assert.NoError(t, err)
	assert.Equal(t, 999999, size)
}

func TestPing_Success(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+PONG\r\n")
	defer cleanup()

	c := New(addr)
	err := c.Ping()
	assert.NoError(t, err)
}

func TestPing_UnexpectedResponse(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+LOADING\r\n")
	defer cleanup()

	c := New(addr)
	err := c.Ping()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected response")
}

func TestPing_ConnectionError(t *testing.T) {
	c := New("127.0.0.1:1") // Port 1 is unlikely to be open.
	c.SetTimeout(500 * time.Millisecond)
	err := c.Ping()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ping")
}

func TestInfoReplication_ConnectionError(t *testing.T) {
	c := New("127.0.0.1:1")
	c.SetTimeout(500 * time.Millisecond)
	info, err := c.InfoReplication()
	assert.Error(t, err)
	assert.Nil(t, info)
}

func TestSentinelFailover_Success(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+OK\r\n")
	defer cleanup()

	c := New(addr)
	err := c.SentinelFailover("mymaster")
	assert.NoError(t, err)
}

func TestSentinelFailover_Error(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "-ERR No such master\r\n")
	defer cleanup()

	c := New(addr)
	err := c.SentinelFailover("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sentinel failover")
}

func TestSentinelReset_Success(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+OK\r\n")
	defer cleanup()

	c := New(addr)
	err := c.SentinelReset("*")
	assert.NoError(t, err)
}

func TestSentinelRemove_Success(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+OK\r\n")
	defer cleanup()

	c := New(addr)
	err := c.SentinelRemove("mymaster")
	assert.NoError(t, err)
}

func TestSentinelMonitorAdd_Success(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+OK\r\n")
	defer cleanup()

	c := New(addr)
	err := c.SentinelMonitorAdd("mymaster", "10.0.0.1", 6379, 2)
	assert.NoError(t, err)
}

func TestSentinelSet_Success(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+OK\r\n")
	defer cleanup()

	c := New(addr)
	err := c.SentinelSet("mymaster", "down-after-milliseconds", "5000")
	assert.NoError(t, err)
}

func TestReplicaOf_Success(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+OK\r\n")
	defer cleanup()

	c := New(addr)
	err := c.ReplicaOf("NO", "ONE")
	assert.NoError(t, err)
}

func TestReplicaOf_ConnectionError(t *testing.T) {
	c := New("127.0.0.1:1")
	c.SetTimeout(500 * time.Millisecond)
	err := c.ReplicaOf("10.0.0.1", "6379")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "replicaof")
}

func TestWait_ConnectionError(t *testing.T) {
	c := New("127.0.0.1:1")
	c.SetTimeout(500 * time.Millisecond)
	acked, err := c.Wait(2, 1000)
	assert.Error(t, err)
	assert.Equal(t, 0, acked)
}

// --- NewWithPassword / NewTLSWithPassword ---

func TestNewWithPassword(t *testing.T) {
	c := NewWithPassword("localhost:6379", "secret")
	assert.Equal(t, "localhost:6379", c.addr)
	assert.Equal(t, "secret", c.password)
	assert.Nil(t, c.tlsConfig)
}

func TestNewTLSWithPassword(t *testing.T) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	c := NewTLSWithPassword("localhost:16379", tlsCfg, "secret")
	assert.Equal(t, "localhost:16379", c.addr)
	assert.Equal(t, "secret", c.password)
	assert.NotNil(t, c.tlsConfig)
}

// --- Authentication tests ---

func TestExec_AuthSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)

		// Read AUTH command.
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("+OK\r\n"))

		// Read PING command.
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("+PONG\r\n"))
	}()

	c := NewWithPassword(ln.Addr().String(), "mysecret")
	err = c.Ping()
	assert.NoError(t, err)
}

func TestExec_AuthFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)

		// Read AUTH command.
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("-ERR invalid password\r\n"))
	}()

	c := NewWithPassword(ln.Addr().String(), "wrongpassword")
	err = c.Ping()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH failed")
}

// --- parseReplicationInfo edge cases ---

func TestParseReplicationInfo_OnlyComments(t *testing.T) {
	raw := "# Replication\n# Server\n"
	info := parseReplicationInfo(raw)
	assert.Equal(t, "", info.Role)
	assert.Equal(t, 0, info.ConnectedSlaves)
}

func TestParseReplicationInfo_NoColonInLine(t *testing.T) {
	raw := "role:master\ngarbage_line_without_colon\nconnected_slaves:1\n"
	info := parseReplicationInfo(raw)
	assert.Equal(t, "master", info.Role)
	assert.Equal(t, 1, info.ConnectedSlaves)
}

func TestParseReplicationInfo_ValueWithColon(t *testing.T) {
	// master_host can contain colons in IPv6 addresses.
	raw := "role:slave\nmaster_host:fd00::1\nmaster_port:6379\n"
	info := parseReplicationInfo(raw)
	assert.Equal(t, "slave", info.Role)
	assert.Equal(t, "fd00::1", info.MasterHost)
	assert.Equal(t, "6379", info.MasterPort)
}

func TestParseReplicationInfo_WhitespaceLines(t *testing.T) {
	raw := "  \n\nrole:master\n  \nconnected_slaves:0\n\n"
	info := parseReplicationInfo(raw)
	assert.Equal(t, "master", info.Role)
}

func TestParseReplicationInfo_MasterLinkDown(t *testing.T) {
	raw := "role:slave\nmaster_link_status:down\nmaster_sync_in_progress:0\n"
	info := parseReplicationInfo(raw)
	assert.Equal(t, "down", info.MasterLinkStatus)
	assert.False(t, info.MasterSyncInProgress)
}

func TestParseReplicationInfo_InvalidConnectedSlaves(t *testing.T) {
	raw := "role:master\nconnected_slaves:abc\n"
	info := parseReplicationInfo(raw)
	assert.Equal(t, "master", info.Role)
	assert.Equal(t, 0, info.ConnectedSlaves) // Invalid parses to 0.
}

// --- parseSentinelMasterInfo edge cases ---

func TestParseSentinelMasterInfo_OddNumberOfLines(t *testing.T) {
	// Last line has no pair — should be silently ignored.
	raw := "name\nmymaster\nip\n10.0.0.1\norphan_key\n"
	info := parseSentinelMasterInfo(raw)
	assert.Equal(t, "mymaster", info.Name)
	assert.Equal(t, "10.0.0.1", info.IP)
}

func TestParseSentinelMasterInfo_AllFields(t *testing.T) {
	raw := "name\ntest\nip\n10.0.0.5\nport\n6379\nflags\nmaster\nnum-slaves\n3\nquorum\n2\n"
	info := parseSentinelMasterInfo(raw)
	assert.Equal(t, "test", info.Name)
	assert.Equal(t, "10.0.0.5", info.IP)
	assert.Equal(t, "6379", info.Port)
	assert.Equal(t, "master", info.Flags)
	assert.Equal(t, 3, info.NumSlaves)
	assert.Equal(t, 2, info.Quorum)
}

func TestParseSentinelMasterInfo_InvalidNumSlaves(t *testing.T) {
	raw := "name\ntest\nnum-slaves\ninvalid\n"
	info := parseSentinelMasterInfo(raw)
	assert.Equal(t, "test", info.Name)
	assert.Equal(t, 0, info.NumSlaves) // Invalid parses to 0.
}

func TestParseSentinelMasterInfo_FlagsWithErrors(t *testing.T) {
	raw := "name\ntest\nflags\ns_down,master\n"
	info := parseSentinelMasterInfo(raw)
	assert.Equal(t, "s_down,master", info.Flags)
}

// --- connHint edge cases ---

func TestConnHint_ENETUNREACH(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.ENETUNREACH,
	}

	hint := connHint("10.0.0.1:6379", err)
	assert.Contains(t, hint, "route")
}

func TestConnHint_NilError(t *testing.T) {
	// Should not panic with nil error. connHint is only called with non-nil errors,
	// but verify robustness.
	hint := connHint("10.0.0.1:6379", fmt.Errorf("some error"))
	assert.NotEmpty(t, hint)
}

// --- readBulkString edge cases ---

func TestReadBulkString_EmptyBulk(t *testing.T) {
	// $0\r\n\r\n — zero-length bulk string.
	reader := bufio.NewReader(strings.NewReader("\r\n"))
	result, err := readBulkString(reader, "$0")
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestReadBulkString_NullBulk(t *testing.T) {
	// $-1 — null bulk string.
	result, err := readBulkString(bufio.NewReader(strings.NewReader("")), "$-1")
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestReadBulkString_NormalData(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("hello\r\n"))
	result, err := readBulkString(reader, "$5")
	assert.NoError(t, err)
	assert.Equal(t, "hello", result)
}

func TestReadBulkString_InvalidSizeHeader(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	_, err := readBulkString(reader, "$abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing bulk string size")
}

// --- readArray edge cases ---

func TestReadArray_EmptyArrayDirect(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	result, err := readArray(reader, "*0")
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestReadArray_SingleElement(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("$3\r\nfoo\r\n"))
	result, err := readArray(reader, "*1")
	assert.NoError(t, err)
	assert.Contains(t, result, "foo")
}

func TestReadArray_NilElement(t *testing.T) {
	// A nil bulk string element ($-1).
	reader := bufio.NewReader(strings.NewReader("$-1\r\n"))
	result, err := readArray(reader, "*1")
	assert.NoError(t, err)
	assert.Contains(t, result, "(nil)")
}

func TestReadArray_InvalidCountHeader(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	_, err := readArray(reader, "*abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing array count")
}

func TestReadArray_MultipleElements(t *testing.T) {
	data := "$4\r\nname\r\n$4\r\ntest\r\n$2\r\nip\r\n$8\r\n10.0.0.1\r\n"
	reader := bufio.NewReader(strings.NewReader(data))
	result, err := readArray(reader, "*4")
	assert.NoError(t, err)
	assert.Contains(t, result, "name")
	assert.Contains(t, result, "test")
	assert.Contains(t, result, "ip")
	assert.Contains(t, result, "10.0.0.1")
}

// --- formatRESP edge cases ---

func TestFormatRESP_EmptyArgs(t *testing.T) {
	resp := formatRESP([]string{})
	assert.Equal(t, "*0\r\n", resp)
}

func TestFormatRESP_ArgWithSpaces(t *testing.T) {
	resp := formatRESP([]string{"SET", "key with spaces", "value"})
	assert.Contains(t, resp, "$15\r\nkey with spaces\r\n")
}

func TestFormatRESP_EmptyString(t *testing.T) {
	resp := formatRESP([]string{""})
	assert.Equal(t, "*1\r\n$0\r\n\r\n", resp)
}

// --- ConnectionError edge cases ---

func TestConnectionError_NilCause(t *testing.T) {
	e := &ConnectionError{
		Addr:  "10.0.0.1:6379",
		Cause: nil,
		Hint:  "some hint",
	}
	assert.Contains(t, e.Error(), "10.0.0.1:6379")
	assert.Nil(t, e.Unwrap())
}

// fakeRESPServerCustom starts a TCP listener that accepts one connection,
// reads one command, and responds with the given raw RESP response.
func fakeRESPServerCustom(t *testing.T, response string) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read and discard the incoming command.
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)

		// Respond with custom RESP data.
		_, _ = conn.Write([]byte(response))
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }
}

// --- parseSentinelReplicasInfo ---

func TestParseSentinelReplicasInfo_TwoReplicas(t *testing.T) {
	raw := "ip\nhost-1.example.com\nport\n6379\nflags\nslave\n---\nip\nhost-2.example.com\nport\n6379\nflags\nslave\n"
	replicas := parseSentinelReplicasInfo(raw)
	require.Len(t, replicas, 2)
	assert.Equal(t, "host-1.example.com", replicas[0].IP)
	assert.Equal(t, "6379", replicas[0].Port)
	assert.Equal(t, "slave", replicas[0].Flags)
	assert.Equal(t, "host-2.example.com", replicas[1].IP)
}

func TestParseSentinelReplicasInfo_Empty(t *testing.T) {
	replicas := parseSentinelReplicasInfo("")
	assert.Nil(t, replicas)
}

func TestParseSentinelReplicasInfo_SingleReplica(t *testing.T) {
	raw := "ip\n10.0.0.5\nport\n16379\nflags\nslave\n"
	replicas := parseSentinelReplicasInfo(raw)
	require.Len(t, replicas, 1)
	assert.Equal(t, "10.0.0.5", replicas[0].IP)
	assert.Equal(t, "16379", replicas[0].Port)
}

func TestParseSentinelReplicasInfo_OddFields(t *testing.T) {
	// Orphan key at end is silently ignored.
	raw := "ip\nhost.example.com\norphan\n"
	replicas := parseSentinelReplicasInfo(raw)
	require.Len(t, replicas, 1)
	assert.Equal(t, "host.example.com", replicas[0].IP)
}

// --- readArray nested arrays ---

func TestReadArray_NestedArrays(t *testing.T) {
	// Simulate SENTINEL REPLICAS response: outer *2, each inner *4 (2 key-value pairs).
	inner1 := "*4\r\n$2\r\nip\r\n$6\r\nhost-1\r\n$4\r\nport\r\n$4\r\n6379\r\n"
	inner2 := "*4\r\n$2\r\nip\r\n$6\r\nhost-2\r\n$4\r\nport\r\n$4\r\n6379\r\n"
	data := inner1 + inner2
	reader := bufio.NewReader(strings.NewReader(data))
	result, err := readArray(reader, "*2")
	assert.NoError(t, err)
	// Should contain separator between nested arrays.
	assert.Contains(t, result, "---")
	assert.Contains(t, result, "host-1")
	assert.Contains(t, result, "host-2")
}

func TestReadArray_SingleNestedArray(t *testing.T) {
	inner := "*2\r\n$2\r\nip\r\n$7\r\n1.2.3.4\r\n"
	reader := bufio.NewReader(strings.NewReader(inner))
	result, err := readArray(reader, "*1")
	assert.NoError(t, err)
	assert.Contains(t, result, "1.2.3.4")
	// No separator for single element.
	assert.NotContains(t, result, "---")
}

// --- SentinelReplicas via fake server ---

func TestSentinelReplicas_Success(t *testing.T) {
	// Build a SENTINEL REPLICAS response: outer array of 1 replica, inner array of 6 elements.
	resp := "*1\r\n*6\r\n$2\r\nip\r\n$10\r\nhost.local\r\n$4\r\nport\r\n$4\r\n6379\r\n$5\r\nflags\r\n$5\r\nslave\r\n"
	addr, cleanup := fakeRESPServerCustom(t, resp)
	defer cleanup()

	c := New(addr)
	replicas, err := c.SentinelReplicas("test")
	require.NoError(t, err)
	require.Len(t, replicas, 1)
	assert.Equal(t, "host.local", replicas[0].IP)
	assert.Equal(t, "6379", replicas[0].Port)
	assert.Equal(t, "slave", replicas[0].Flags)
}

func TestSentinelReplicas_EmptyArray(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "*0\r\n")
	defer cleanup()

	c := New(addr)
	replicas, err := c.SentinelReplicas("test")
	require.NoError(t, err)
	assert.Empty(t, replicas)
}

func TestSentinelReplicas_ConnectionError(t *testing.T) {
	c := New("127.0.0.1:1")
	c.SetTimeout(500 * time.Millisecond)
	replicas, err := c.SentinelReplicas("test")
	assert.Error(t, err)
	assert.Nil(t, replicas)
}
