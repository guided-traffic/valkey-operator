package valkeyclient

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The replies a Sentinel gives to a client whose credential does not match its own
// requirepass, measured on valkey/valkey:9.2.0-alpine.
const (
	replyNoPasswordConfigured = "-ERR AUTH <password> called without any password configured for the default user. " +
		"Are you sure your configuration is correct?\r\n"
	replyNoAuth    = "-NOAUTH Authentication required.\r\n"
	replyWrongPass = "-WRONGPASS invalid username-password pair or user is disabled.\r\n"
)

// passwordlessSentinel answers like a Sentinel without requirepass: AUTH is refused,
// every other command succeeds.
func passwordlessSentinel(args []string) string {
	if args[0] == "AUTH" {
		return replyNoPasswordConfigured
	}
	return "+OK\r\n"
}

// sentinelRequirepass is the requirepass of the Sentinel requirepassSentinel emulates.
const sentinelRequirepass = "pw"

// requirepassSentinel answers like a Sentinel with requirepass sentinelRequirepass. The
// client opens one connection per command and sends AUTH first on it, so with one
// client issuing commands one after another "the previous command was a successful
// AUTH" is "this connection is authenticated".
func requirepassSentinel() respScript {
	var mu sync.Mutex
	authenticated := false
	return func(args []string) string {
		mu.Lock()
		defer mu.Unlock()
		if args[0] == "AUTH" {
			if len(args) == 2 && args[1] == sentinelRequirepass {
				authenticated = true
				return "+OK\r\n"
			}
			return replyWrongPass
		}
		if !authenticated {
			return replyNoAuth
		}
		authenticated = false
		return "+OK\r\n"
	}
}

func TestSentinelAuthFallback_PasswordlessSentinelTakesTheCommandWithoutAuth(t *testing.T) {
	// The spec asks for Sentinel auth (disableAuth switched to false); the Sentinel
	// still runs without requirepass until the Sentinel roll replaces it.
	srv := newScriptedServer(t, passwordlessSentinel)

	err := NewWithPassword(srv.addr, "pw").WithSentinelAuthFallback("pw").SentinelFailover("m")

	require.NoError(t, err)
	assert.Equal(t, []string{"AUTH pw", "SENTINEL FAILOVER m"}, srv.commands(),
		"the command is sent again, without AUTH, after the Sentinel said it has no password")
	assert.Equal(t, 2, srv.connections(), "the retry runs on a fresh connection")
}

func TestSentinelAuthFallback_RequirepassSentinelTakesTheCommandWithTheClusterPassword(t *testing.T) {
	// The spec disables Sentinel auth (disableAuth switched to true); the Sentinel
	// still requires the cluster password it booted with.
	srv := newScriptedServer(t, requirepassSentinel())

	err := New(srv.addr).WithSentinelAuthFallback("pw").SentinelFailover("m")

	require.NoError(t, err)
	assert.Equal(t, []string{"SENTINEL FAILOVER m", "AUTH pw", "SENTINEL FAILOVER m"}, srv.commands(),
		"the refused command executed nothing, and is sent again after AUTH with the cluster password")
}

func TestSentinelAuthFallback_WithoutTheFallbackAMismatchFails(t *testing.T) {
	// The behaviour of every connection the fallback is not set on, data nodes
	// included: a credential mismatch is returned, nothing is retried.
	t.Run("password sent to a passwordless server", func(t *testing.T) {
		srv := newScriptedServer(t, passwordlessSentinel)

		err := NewWithPassword(srv.addr, "pw").SentinelFailover("m")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "without any password configured")
		assert.Equal(t, []string{"AUTH pw"}, srv.commands())
	})
	t.Run("no password sent to a requirepass server", func(t *testing.T) {
		srv := newScriptedServer(t, requirepassSentinel())

		err := New(srv.addr).SentinelFailover("m")

		require.Error(t, err)
		var reply *ReplyError
		require.True(t, errors.As(err, &reply))
		assert.Equal(t, "NOAUTH", reply.Code())
		assert.Equal(t, []string{"SENTINEL FAILOVER m"}, srv.commands())
	})
}

func TestSentinelAuthFallback_RetriesOnlyACredentialMismatch(t *testing.T) {
	cases := []struct {
		name            string
		password        string
		clusterPassword string
		script          respScript
		wantCommands    []string
	}{
		{
			// A wrong password is not a switched setting: the Sentinel has one, and the
			// spec's is not it. Sending none would only turn WRONGPASS into NOAUTH.
			name:            "wrong password",
			password:        "stale",
			clusterPassword: "stale",
			script:          requirepassSentinel(),
			wantCommands:    []string{"AUTH stale"},
		},
		{
			// Auth is off in the spec: there is no password this cluster uses, so the
			// client has nothing to send and does not invent one.
			name:            "NOAUTH without a cluster password",
			password:        "",
			clusterPassword: "",
			script:          requirepassSentinel(),
			wantCommands:    []string{"SENTINEL FAILOVER m"},
		},
		{
			name:            "another error reply",
			password:        "",
			clusterPassword: "pw",
			script:          func([]string) string { return "-INPROG Failover already in progress\r\n" },
			wantCommands:    []string{"SENTINEL FAILOVER m"},
		},
		{
			// The cluster password is refused as well: one retry, never a loop.
			name:            "the retry is refused too",
			password:        "",
			clusterPassword: "stale",
			script:          requirepassSentinel(),
			wantCommands:    []string{"SENTINEL FAILOVER m", "AUTH stale"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newScriptedServer(t, tc.script)

			c := New(srv.addr)
			if tc.password != "" {
				c = NewWithPassword(srv.addr, tc.password)
			}
			err := c.WithSentinelAuthFallback(tc.clusterPassword).SentinelFailover("m")

			require.Error(t, err)
			assert.Equal(t, tc.wantCommands, srv.commands())
		})
	}
}

func TestSentinelAuthFallback_ConnectionFailureIsNotRetried(t *testing.T) {
	c := NewWithPassword("127.0.0.1:1", "pw").WithSentinelAuthFallback("pw")
	c.SetTimeout(200 * time.Millisecond)

	err := c.Ping()

	require.Error(t, err)
	var connErr *ConnectionError
	assert.True(t, errors.As(err, &connErr), "a dial failure is returned as it was, not as a retry's error")
}
