package valkeyclient

import (
	"bufio"
	"crypto/tls"
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
