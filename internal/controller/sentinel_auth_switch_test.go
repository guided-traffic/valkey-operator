package controller

import (
	"context"
	"crypto/tls"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// A switch of spec.sentinel.disableAuth changes the operator's Sentinel credential at
// once, while every running Sentinel keeps the requirepass it booted with until the
// Sentinel roll replaces it -- and that roll waits for the data tier, whose master
// handover needs a Sentinel failover. Measured on a live cluster (true -> false): every
// SENTINEL command was refused, the data roll cycled through trigger, reset and
// retrigger without end, and the Sentinel tier never rolled
// (docs/adr/0016-authentication-and-tls-posture.md, D9).

const switchPassword = "supersecret"

// authSwitchCR is a three-pod Sentinel cluster with auth from switchPassword's Secret.
func authSwitchCR(name string, disableAuth bool) (*vkov1.Valkey, *corev1.Secret) {
	v := sentinelClusterCR(name, 3, func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: name + "-auth", SecretPasswordKey: "password"}
		v.Spec.Sentinel.DisableAuth = disableAuth
	})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-auth", Namespace: v.Namespace},
		Data:       map[string][]byte{"password": []byte(switchPassword)},
	}
	return v, secret
}

// attachWithPassword routes every client into the router like attach, but keeps the
// password the operator chose, so the router sees the AUTH a real Sentinel would.
func attachWithPassword(rr *respRouter, r *ValkeyReconciler) {
	r.NewValkeyClientFn = func(addr, password string, _ *tls.Config) *valkeyclient.Client {
		if password == "" {
			return valkeyclient.New(rr.listenerFor(addr))
		}
		return valkeyclient.NewWithPassword(rr.listenerFor(addr), password)
	}
}

func TestTriggerSentinelFailover_ReachesSentinelsStillWithoutRequirepass(t *testing.T) {
	// disableAuth switched true -> false: the spec asks for AUTH, the Sentinels have no
	// password yet. The reply is the one valkey/valkey:9.2.0-alpine gave on the cluster.
	v, secret := authSwitchCR("auth-on", false)
	r, _ := newTestReconciler(v, secret)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if strings.EqualFold(args[0], "AUTH") {
			return respReply("ERR AUTH <password> called without any password configured for the default user. " +
				"Are you sure your configuration is correct?")
		}
		return respOK
	})
	attachWithPassword(router, r)

	require.NoError(t, r.triggerSentinelFailover(context.Background(), v, true),
		"a Sentinel that still runs without requirepass takes the failover")

	sentinel0 := sentinelAddr(v, 0, builder.SentinelPort)
	assert.Equal(t, []string{"AUTH " + switchPassword, coordinatedCmd(v)}, router.commandsTo(sentinel0),
		"AUTH as the spec says, refused, then the failover without AUTH")
}

func TestTriggerSentinelFailover_ReachesSentinelsStillWithRequirepass(t *testing.T) {
	// disableAuth switched false -> true: the spec drops AUTH, the Sentinels still
	// require the cluster password. One client issues one command per connection, AUTH
	// first, so "the previous command was a good AUTH" is "this connection is
	// authenticated".
	v, secret := authSwitchCR("auth-off", true)
	r, _ := newTestReconciler(v, secret)
	var mu sync.Mutex
	authenticated := false
	router := newRESPRouter(t, func(_ string, args []string) string {
		mu.Lock()
		defer mu.Unlock()
		if strings.EqualFold(args[0], "AUTH") {
			authenticated = len(args) == 2 && args[1] == switchPassword
			if authenticated {
				return respOK
			}
			return respReply("WRONGPASS invalid username-password pair or user is disabled.")
		}
		if !authenticated {
			return respReply("NOAUTH Authentication required.")
		}
		authenticated = false
		return respOK
	})
	attachWithPassword(router, r)

	require.NoError(t, r.triggerSentinelFailover(context.Background(), v, true),
		"a Sentinel that still requires the cluster password takes the failover")

	sentinel0 := sentinelAddr(v, 0, builder.SentinelPort)
	assert.Equal(t, []string{coordinatedCmd(v), "AUTH " + switchPassword, coordinatedCmd(v)},
		router.commandsTo(sentinel0),
		"the failover without AUTH as the spec says, refused unexecuted, then with the cluster password")
}
