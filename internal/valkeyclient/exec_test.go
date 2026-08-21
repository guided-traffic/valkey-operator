package valkeyclient

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// A scripted RESP server for the multi-command paths.
//
// ExecMulti and ExecGet are the only entry points that keep one connection open
// across several commands, so the properties worth pinning are per-connection:
// which commands arrived, in which order, on which connection, and what happens
// to the commands behind one that failed. recordingRESPServer answers +OK to
// everything and cannot express a failing command, so the server below scripts
// the reply per command and remembers the connection each command arrived on.
// ---------------------------------------------------------------------------

// respScript answers one decoded command. Returning an empty string makes the
// server hang up without replying, which is what a peer dying mid-sequence
// looks like to the client.
type respScript func(args []string) string

type recordedCommand struct {
	conn int
	args []string
}

type scriptedServer struct {
	addr string

	mu       sync.Mutex
	received []recordedCommand
}

// newScriptedServer starts a loopback listener that answers every command with
// the reply script returns for it.
func newScriptedServer(t *testing.T, script respScript) *scriptedServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	s := &scriptedServer{addr: ln.Addr().String()}

	go func() {
		connID := 0
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			connID++
			go s.serve(conn, connID, script)
		}
	}()

	return s
}

func (s *scriptedServer) serve(conn net.Conn, connID int, script respScript) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	for {
		args, err := readRESPCommand(reader)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.received = append(s.received, recordedCommand{conn: connID, args: args})
		s.mu.Unlock()

		reply := script(args)
		if reply == "" {
			return
		}
		if _, writeErr := conn.Write([]byte(reply)); writeErr != nil {
			return
		}
	}
}

// commands returns the commands the server decoded, joined by spaces.
func (s *scriptedServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.received))
	for _, cmd := range s.received {
		out = append(out, strings.Join(cmd.args, " "))
	}
	return out
}

// connections returns the number of distinct connections commands arrived on.
func (s *scriptedServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[int]bool{}
	for _, cmd := range s.received {
		seen[cmd.conn] = true
	}
	return len(seen)
}

// readRESPCommand decodes one RESP array of bulk strings.
func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	header = strings.TrimRight(header, "\r\n")
	if !strings.HasPrefix(header, "*") {
		return nil, fmt.Errorf("not a RESP array: %q", header)
	}
	count, err := strconv.Atoi(header[1:])
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		sizeLine, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil, readErr
		}
		size, convErr := strconv.Atoi(strings.TrimRight(sizeLine, "\r\n")[1:])
		if convErr != nil {
			return nil, convErr
		}
		buf := make([]byte, size+2) // payload plus CRLF
		if _, readErr = io.ReadFull(reader, buf); readErr != nil {
			return nil, readErr
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

// authVerb is the command the client sends before anything else when a password
// is configured.
const authVerb = "AUTH"

// alwaysOK answers every command with +OK.
func alwaysOK(_ []string) string { return "+OK\r\n" }

// replyPerVerb answers each command by its verb, falling back to +OK.
func replyPerVerb(replies map[string]string) respScript {
	return func(args []string) string {
		if reply, ok := replies[strings.ToUpper(args[0])]; ok {
			return reply
		}
		return "+OK\r\n"
	}
}

// stalledServerAddr accepts connections and then reads nothing at all, so the
// client's socket buffer fills and its write runs into the deadline.
func stalledServerAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				<-done
				_ = conn.Close()
			}()
		}
	}()

	return ln.Addr().String()
}

// oversizedValue is larger than any socket buffer, so writing it to a peer that
// never reads cannot complete.
func oversizedValue() string { return strings.Repeat("x", 8<<20) }

// --- ExecMulti ---

// The whole point of ExecMulti is that SELECT and the command it scopes share
// one connection: on separate connections the SET would land in database 0.
func TestExecMulti_RunsEveryCommandOnASingleConnection(t *testing.T) {
	server := newScriptedServer(t, alwaysOK)

	err := New(server.addr).ExecMulti(
		[]string{"SELECT", "3"},
		[]string{"SET", "probe-key", "probe-value"},
		[]string{"EXPIRE", "probe-key", "60"},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"SELECT 3",
		"SET probe-key probe-value",
		"EXPIRE probe-key 60",
	}, server.commands(), "every command is sent, in order")
	assert.Equal(t, 1, server.connections(), "SELECT only scopes commands on its own connection")
}

func TestExecMulti_AuthenticatesOnTheSameConnectionFirst(t *testing.T) {
	server := newScriptedServer(t, alwaysOK)

	err := NewWithPassword(server.addr, "s3cret").ExecMulti(
		[]string{"SELECT", "0"},
		[]string{"SET", "k", "v"},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"AUTH s3cret", "SELECT 0", "SET k v"}, server.commands(),
		"AUTH precedes the commands it authorises")
	assert.Equal(t, 1, server.connections())
}

// A rejected AUTH must abort before any command is sent, or the commands run
// unauthenticated against a server that happens to allow them.
func TestExecMulti_RejectedAuthSendsNoCommand(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		authVerb: "-WRONGPASS invalid username-password pair\r\n",
	}))

	err := NewWithPassword(server.addr, "wrong").ExecMulti([]string{"SET", "k", "v"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH failed on "+server.addr)
	assert.Equal(t, []string{"AUTH wrong"}, server.commands(),
		"the SET must never reach a server that refused the password")
}

// An error on one command has to stop the sequence: the commands behind it were
// written on the assumption that the earlier ones succeeded.
func TestExecMulti_StopsAtTheFirstRejectedCommand(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		"SET": "-ERR value is not an integer or out of range\r\n",
	}))

	err := New(server.addr).ExecMulti(
		[]string{"SELECT", "0"},
		[]string{"SET", "k", "v"},
		[]string{"EXPIRE", "k", "60"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command SET on "+server.addr)
	assert.Contains(t, err.Error(), "value is not an integer")
	assert.Equal(t, []string{"SELECT 0", "SET k v"}, server.commands(),
		"the command behind the failing one must not be sent")
}

func TestExecMulti_PeerHangingUpMidSequenceIsAnError(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		"SET": "", // the server dies instead of answering
	}))

	err := New(server.addr).ExecMulti(
		[]string{"SELECT", "0"},
		[]string{"SET", "k", "v"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command SET on "+server.addr)
	assert.ErrorIs(t, err, io.EOF)
}

func TestExecMulti_UnreachableServerReportsTheConnectionFailure(t *testing.T) {
	c := New(unreachablePort)
	c.SetTimeout(500 * time.Millisecond)

	err := c.ExecMulti([]string{"SET", "k", "v"})

	require.Error(t, err)
	var connErr *ConnectionError
	require.ErrorAs(t, err, &connErr)
	assert.Equal(t, unreachablePort, connErr.Addr)
	assert.Contains(t, connErr.Hint, "refused")
}

// The deadline covers the whole sequence, so a peer that stops reading fails the
// write instead of blocking the reconcile forever.
func TestExecMulti_StalledPeerFailsTheWriteWithinTheTimeout(t *testing.T) {
	c := New(stalledServerAddr(t))
	c.SetTimeout(300 * time.Millisecond)

	start := time.Now()
	err := c.ExecMulti([]string{"SET", "k", oversizedValue()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sending command SET")
	assert.Less(t, time.Since(start), 5*time.Second, "the write must not outlive the deadline")
}

// --- ExecGet ---

func TestExecGet_ReturnsTheLastResponse(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		"GET": "$11\r\nprobe-value\r\n",
	}))

	value, err := New(server.addr).ExecGet(
		[]string{"SELECT", "3"},
		[]string{"GET", "probe-key"},
	)

	require.NoError(t, err)
	assert.Equal(t, "probe-value", value, "the GET reply wins over the +OK of the SELECT")
	assert.Equal(t, []string{"SELECT 3", "GET probe-key"}, server.commands())
	assert.Equal(t, 1, server.connections(), "the GET must read the database the SELECT chose")
}

// A key that does not exist is an empty value, not an error: the caller cannot
// tell "missing" from "failed" if this returns an error.
func TestExecGet_MissingKeyIsAnEmptyValue(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		"GET": "$-1\r\n",
	}))

	value, err := New(server.addr).ExecGet(
		[]string{"SELECT", "0"},
		[]string{"GET", "absent"},
	)

	require.NoError(t, err)
	assert.Equal(t, "", value)
}

func TestExecGet_AuthenticatesOnTheSameConnectionFirst(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		"GET": "$1\r\nv\r\n",
	}))

	value, err := NewWithPassword(server.addr, "s3cret").ExecGet(
		[]string{"SELECT", "0"},
		[]string{"GET", "k"},
	)

	require.NoError(t, err)
	assert.Equal(t, "v", value)
	assert.Equal(t, []string{"AUTH s3cret", "SELECT 0", "GET k"}, server.commands())
	assert.Equal(t, 1, server.connections())
}

// A server that answers AUTH with something other than OK is not authenticated,
// even though the reply is not a RESP error.
func TestExecGet_AuthAnsweredWithoutOKIsRejected(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		authVerb: "+PONG\r\n",
	}))

	value, err := NewWithPassword(server.addr, "s3cret").ExecGet([]string{"GET", "k"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUTH failed on "+server.addr)
	assert.Contains(t, err.Error(), "PONG")
	assert.Empty(t, value)
	assert.Equal(t, []string{"AUTH s3cret"}, server.commands())
}

func TestExecGet_StopsAtTheFirstRejectedCommand(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		"SELECT": "-ERR DB index is out of range\r\n",
	}))

	value, err := New(server.addr).ExecGet(
		[]string{"SELECT", "99"},
		[]string{"GET", "k"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command SELECT on "+server.addr)
	assert.Contains(t, err.Error(), "DB index is out of range")
	assert.Empty(t, value, "no value may be reported when the sequence failed")
	assert.Equal(t, []string{"SELECT 99"}, server.commands())
}

func TestExecGet_PeerHangingUpMidSequenceIsAnError(t *testing.T) {
	server := newScriptedServer(t, replyPerVerb(map[string]string{
		"GET": "",
	}))

	value, err := New(server.addr).ExecGet(
		[]string{"SELECT", "0"},
		[]string{"GET", "k"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command GET on "+server.addr)
	assert.ErrorIs(t, err, io.EOF)
	assert.Empty(t, value)
}

func TestExecGet_UnreachableServerReportsTheConnectionFailure(t *testing.T) {
	c := New(unreachablePort)
	c.SetTimeout(500 * time.Millisecond)

	value, err := c.ExecGet([]string{"GET", "k"})

	require.Error(t, err)
	var connErr *ConnectionError
	require.ErrorAs(t, err, &connErr)
	assert.Equal(t, unreachablePort, connErr.Addr)
	assert.Empty(t, value)
}

func TestExecGet_StalledPeerFailsTheWriteWithinTheTimeout(t *testing.T) {
	c := New(stalledServerAddr(t))
	c.SetTimeout(300 * time.Millisecond)

	start := time.Now()
	value, err := c.ExecGet([]string{"SET", "k", oversizedValue()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sending command SET")
	assert.Empty(t, value)
	assert.Less(t, time.Since(start), 5*time.Second, "the write must not outlive the deadline")
}

func TestExecGet_WithoutCommandsReturnsNothing(t *testing.T) {
	server := newScriptedServer(t, alwaysOK)

	value, err := New(server.addr).ExecGet()

	require.NoError(t, err)
	assert.Empty(t, value)
	assert.Empty(t, server.commands())
}

// --- error wrapping of the single-command helpers ---

// unreachablePort refuses instantly.
const unreachablePort = "127.0.0.1:1"

// Every command must name itself and the address it failed against; a bare
// "connection refused" in the operator log does not say which pod or which step.
func TestCommands_WrapTheConnectionFailureWithCommandAndAddress(t *testing.T) {
	tests := []struct {
		name    string
		call    func(*Client) error
		wantMsg string
	}{
		{
			name:    "sentinel master",
			call:    func(c *Client) error { _, err := c.SentinelMaster("mymaster"); return err },
			wantMsg: "sentinel master mymaster on " + unreachablePort,
		},
		{
			name:    "sentinel reset",
			call:    func(c *Client) error { return c.SentinelReset("*") },
			wantMsg: "sentinel reset * on " + unreachablePort,
		},
		{
			name:    "sentinel remove",
			call:    func(c *Client) error { return c.SentinelRemove("mymaster") },
			wantMsg: "sentinel remove mymaster on " + unreachablePort,
		},
		{
			name:    "sentinel monitor",
			call:    func(c *Client) error { return c.SentinelMonitorAdd("mymaster", "10.0.0.1", 6379, 2) },
			wantMsg: "sentinel monitor mymaster 10.0.0.1:6379 on " + unreachablePort,
		},
		{
			name:    "sentinel set",
			call:    func(c *Client) error { return c.SentinelSet("mymaster", "auth-pass", "s3cret") },
			wantMsg: "sentinel set mymaster auth-pass s3cret on " + unreachablePort,
		},
		{
			name:    "dbsize",
			call:    func(c *Client) error { _, err := c.DBSize(); return err },
			wantMsg: "dbsize on " + unreachablePort,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := New(unreachablePort)
			c.SetTimeout(500 * time.Millisecond)

			err := tc.call(c)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
			var connErr *ConnectionError
			assert.ErrorAs(t, err, &connErr, "the transport failure must stay in the chain")
		})
	}
}

// A reply that is not the integer these two commands promise must be an error,
// not a silent zero: zero keys and zero acknowledgements both read as "healthy
// but empty" further up.
func TestIntegerCommands_RejectNonNumericReplies(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		call    func(*Client) error
		wantMsg string
	}{
		{
			name:    "wait",
			reply:   "+OK\r\n",
			call:    func(c *Client) error { _, err := c.Wait(2, 100); return err },
			wantMsg: `parsing wait response "OK"`,
		},
		{
			name:    "dbsize",
			reply:   "+unknown\r\n",
			call:    func(c *Client) error { _, err := c.DBSize(); return err },
			wantMsg: `parsing dbsize response "unknown"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, cleanup := fakeRESPServerCustom(t, tc.reply)
			defer cleanup()

			err := tc.call(New(addr))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

// A TLS client that reaches a plaintext port must say so: the generic
// "check your firewall" hint sends the operator after the wrong problem.
func TestDial_TLSClientAgainstPlaintextPortReportsAHandshakeFailure(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "+PONG\r\n")
	defer cleanup()

	c := NewTLS(addr, &tls.Config{MinVersion: tls.VersionTLS12})
	c.SetTimeout(2 * time.Second)

	err := c.Ping()

	require.Error(t, err)
	var connErr *ConnectionError
	require.ErrorAs(t, err, &connErr)
	assert.Contains(t, connErr.Hint, "TLS handshake with "+addr+" failed")
}

// A response that is cut short must surface as an error rather than as a
// half-parsed struct with silently missing fields.
func TestReadFullResponse_TruncatedRepliesAreErrors(t *testing.T) {
	tests := []struct {
		name  string
		reply string
	}{
		{name: "bulk string shorter than its header promises", reply: "$64\r\nrole:master\r\n"},
		{name: "array with fewer elements than its header promises", reply: "*4\r\n$4\r\nname\r\n"},
		{name: "array element cut off inside its bulk string", reply: "*2\r\n$4\r\nname\r\n$32\r\nmy\r\n"},
		{name: "nested array cut off", reply: "*1\r\n*2\r\n$2\r\nip\r\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, cleanup := fakeRESPServerCustom(t, tc.reply)
			defer cleanup()

			info, err := New(addr).SentinelMaster("mymaster")

			require.Error(t, err)
			assert.Nil(t, info)
		})
	}
}

// An inline reply carries no RESP type marker; it is handed back verbatim.
func TestReadFullResponse_InlineReplyIsPassedThrough(t *testing.T) {
	addr, cleanup := fakeRESPServerCustom(t, "PONG\r\n")
	defer cleanup()

	require.NoError(t, New(addr).Ping())
}

// A SENTINEL REPLICAS answer whose separators leave an empty block must not
// produce a replica entry with empty ip and port.
func TestParseSentinelReplicasInfo_EmptyBlocksAreDropped(t *testing.T) {
	replicas := parseSentinelReplicasInfo("---\nip\n10.0.0.2\nport\n6379\nflags\nslave\n---\n")

	require.Len(t, replicas, 1)
	assert.Equal(t, "10.0.0.2", replicas[0].IP)
	assert.Equal(t, "6379", replicas[0].Port)
}
