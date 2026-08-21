package health

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// ---------------------------------------------------------------------------
// Harness
//
// Everything downstream of a pod that actually answers — the master's INFO, the
// split-brain arbitration in findMaster, the Sentinel quorum count — used to be
// unobservable: Checker built its clients itself, so every probe left for a pod
// FQDN that does not resolve. Checker.NewValkeyClientFn is the seam
// ValkeyReconciler already has; the router below uses it to put a real RESP
// listener behind each pod name while keeping the address the Checker derived
// visible for assertions.
// ---------------------------------------------------------------------------

// respondFn answers one RESP request. The raw request text is passed through so
// a responder can treat commands differently. Returning an empty string makes
// the server hang up without answering, which is how a pod that dies
// mid-conversation looks to the Checker.
type respondFn func(request string) string

// respServer is a loopback RESP listener standing in for one Valkey or Sentinel
// pod.
type respServer struct {
	addr string
}

// newRESPServer starts a listener that answers every request with respond.
func newRESPServer(t *testing.T, respond respondFn) *respServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				buf := make([]byte, 4096)
				for {
					n, readErr := conn.Read(buf)
					if readErr != nil {
						return
					}
					reply := respond(string(buf[:n]))
					if reply == "" {
						return
					}
					if _, writeErr := conn.Write([]byte(reply)); writeErr != nil {
						return
					}
				}
			}()
		}
	}()

	return &respServer{addr: ln.Addr().String()}
}

// unreachableAddr refuses instantly, standing in for a pod that is Running but
// not answering on its Valkey port.
const unreachableAddr = "127.0.0.1:1"

// probeRouter stands in for the cluster network. It records the address, the
// password and the TLS mode of every probe the Checker builds, and redirects
// the probe at the listener registered for that pod — or at a closed port when
// the pod has no listener.
type probeRouter struct {
	mu        sync.Mutex
	pods      map[string]string
	dialed    []string
	passwords []string
	tlsUsed   []bool
}

func newProbeRouter() *probeRouter {
	return &probeRouter{pods: map[string]string{}}
}

// serve puts a RESP listener behind one pod name.
func (r *probeRouter) serve(t *testing.T, podName string, respond respondFn) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pods[podName] = newRESPServer(t, respond).addr
}

// install wires the router into a Checker and returns it.
func (r *probeRouter) install(c *Checker) *Checker {
	c.NewValkeyClientFn = r.newClient
	return c
}

func (r *probeRouter) newClient(addr, password string, tlsConfig *tls.Config) *valkeyclient.Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dialed = append(r.dialed, addr)
	r.passwords = append(r.passwords, password)
	r.tlsUsed = append(r.tlsUsed, tlsConfig != nil)

	target, ok := r.pods[podNameOf(addr)]
	if !ok {
		return valkeyclient.New(unreachableAddr)
	}
	if password != "" {
		return valkeyclient.NewWithPassword(target, password)
	}
	return valkeyclient.New(target)
}

// dialedAddrs returns the addresses the Checker derived, in probe order.
func (r *probeRouter) dialedAddrs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dialed...)
}

// podNameOf strips the headless-service suffix and port from a pod FQDN.
func podNameOf(addr string) string {
	return strings.SplitN(addr, ".", 2)[0]
}

// --- RESP reply builders ---

func respBulk(payload string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(payload), payload)
}

func respArray(items ...string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d\r\n", len(items))
	for _, item := range items {
		sb.WriteString(respBulk(item))
	}
	return sb.String()
}

// infoReply builds an INFO replication payload as a RESP bulk string.
func infoReply(lines ...string) string {
	return respBulk("# Replication\r\n" + strings.Join(lines, "\r\n") + "\r\n")
}

// masterInfo is the INFO replication payload of a master with n replicas attached.
func masterInfo(connectedSlaves int) string {
	return infoReply("role:master", fmt.Sprintf("connected_slaves:%d", connectedSlaves))
}

// replicaInfo is the INFO replication payload of a replica synced to a master.
func replicaInfo() string {
	return infoReply("role:slave", "master_link_status:up", "connected_slaves:0")
}

// sentinelMasterReply is the SENTINEL MASTER response of a Sentinel that agrees
// on the master.
func sentinelMasterReply(flags string) string {
	return respArray("name", "mymaster", "ip", "10.0.0.1", "port", "6379",
		"flags", flags, "num-slaves", "2", "quorum", "2")
}

// answers replies to AUTH with +OK and to every other command with reply.
func answers(reply string) respondFn {
	return func(request string) string {
		if strings.Contains(strings.ToUpper(request), "AUTH") {
			return "+OK\r\n"
		}
		return reply
	}
}

// diesAfterFirstAnswer answers the first command and hangs up on every later
// one, which is what a master that is killed between two probes looks like.
func diesAfterFirstAnswer(reply string) respondFn {
	var mu sync.Mutex
	answered := false
	return func(string) string {
		mu.Lock()
		defer mu.Unlock()
		if answered {
			return ""
		}
		answered = true
		return reply
	}
}

// runningTestPods returns n Running data pods of the "test" cluster.
func runningTestPods(n int) []client.Object {
	objs := make([]client.Object, 0, n)
	for i := 0; i < n; i++ {
		objs = append(objs, valkeyPodObj(fmt.Sprintf("test-%d", i), corev1.PodRunning))
	}
	return objs
}

func noSentinel(v *vkov1.Valkey) { v.Spec.Sentinel = nil }

// --- CheckCluster with a master that answers ---

func TestCheckCluster_ReportsTheMasterAndItsSyncedReplicas(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel)

	router := newProbeRouter()
	router.serve(t, "test-0", answers(replicaInfo()))
	router.serve(t, "test-1", answers(masterInfo(2)))
	router.serve(t, "test-2", answers(replicaInfo()))

	state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)

	require.NoError(t, state.Error)
	assert.Equal(t, "test-1", state.MasterPod, "the pod reporting role:master is the master")
	assert.Equal(t, "test-1.test-headless.default.svc.cluster.local:6379", state.MasterAddress)
	assert.Equal(t, int32(2), state.TotalReplicas)
	assert.Equal(t, int32(2), state.ReadyReplicas, "connected_slaves is the master's own count")
	assert.True(t, state.AllSynced)
	assert.False(t, state.SentinelMonitoring, "sentinel is disabled, so it is never consulted")
	assert.Equal(t, []string{
		"test-0.test-headless.default.svc.cluster.local:6379",
		"test-1.test-headless.default.svc.cluster.local:6379",
		"test-2.test-headless.default.svc.cluster.local:6379",
		// The master is dialled a second time for its own replication info.
		"test-1.test-headless.default.svc.cluster.local:6379",
	}, router.dialedAddrs())
}

func TestCheckCluster_ReplicaAccounting(t *testing.T) {
	tests := []struct {
		name              string
		master            string
		wantReadyReplicas int32
		wantAllSynced     bool
	}{
		{
			name:              "every replica attached",
			master:            masterInfo(2),
			wantReadyReplicas: 2,
			wantAllSynced:     true,
		},
		{
			name:              "one replica still missing",
			master:            masterInfo(1),
			wantReadyReplicas: 1,
			wantAllSynced:     false,
		},
		{
			name:              "no replica attached at all",
			master:            masterInfo(0),
			wantReadyReplicas: 0,
			wantAllSynced:     false,
		},
		{
			name: "a full sync in progress is not synced",
			master: infoReply("role:master", "connected_slaves:2",
				"master_sync_in_progress:1"),
			wantReadyReplicas: 2,
			wantAllSynced:     false,
		},
		{
			name:              "more attached replicas than the spec expects are clamped",
			master:            masterInfo(7),
			wantReadyReplicas: 2,
			wantAllSynced:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newProbeContext(t)
			v := newTestValkey("test", "default", noSentinel)

			router := newProbeRouter()
			router.serve(t, "test-0", answers(tc.master))

			state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)

			require.NoError(t, state.Error)
			assert.Equal(t, "test-0", state.MasterPod)
			assert.Equal(t, tc.wantReadyReplicas, state.ReadyReplicas)
			assert.Equal(t, tc.wantAllSynced, state.AllSynced)
		})
	}
}

// A master that answers findMaster and is gone by the second probe must surface
// as a health-check error, not as a cluster with zero ready replicas.
func TestCheckCluster_MasterLostBetweenTheTwoProbes(t *testing.T) {
	ctx, capture := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel)

	router := newProbeRouter()
	router.serve(t, "test-0", diesAfterFirstAnswer(masterInfo(2)))

	state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)

	require.Error(t, state.Error)
	assert.Contains(t, state.Error.Error(), "master replication info:")
	assert.Equal(t, "test-0", state.MasterPod, "the master found first is still reported")
	assert.Zero(t, state.ReadyReplicas)
	assert.False(t, state.AllSynced)
	assert.Contains(t, capture.joined(), "Could not get master replication info")
}

// The password from the auth Secret has to reach every probe, including the
// second one against the master.
func TestCheckCluster_AuthenticatedClusterSendsThePasswordOnEveryProbe(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel, func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "test-auth", SecretPasswordKey: "password"}
	})

	objs := append(runningTestPods(3), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-auth", Namespace: testNamespace},
		Data:       map[string][]byte{"password": []byte("s3cret")},
	})

	router := newProbeRouter()
	router.serve(t, "test-0", answers(masterInfo(2)))

	state := router.install(newFakeChecker(objs...)).CheckCluster(ctx, v)

	require.NoError(t, state.Error)
	assert.Equal(t, "test-0", state.MasterPod, "an authenticated master is still discovered")
	router.mu.Lock()
	defer router.mu.Unlock()
	for i, password := range router.passwords {
		assert.Equal(t, "s3cret", password, "probe %d must carry the auth password", i)
	}
}

// TestCheckCluster_TLSEnabledDialsTheTLSPort pins the port switch: with TLS on,
// the health check must reach pods on 16379, not 6379. The router records the
// address the Checker derived, so the assertion is on the Checker itself.
func TestCheckCluster_TLSEnabledDialsTheTLSPort(t *testing.T) {
	ctx, capture := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel, withCertManagerTLS)

	router := newProbeRouter()
	router.serve(t, "test-0", answers(masterInfo(2)))

	objs := append(runningTestPods(3), valkeyCASecret())
	state := router.install(newFakeChecker(objs...)).CheckCluster(ctx, v)

	require.NoError(t, state.Error)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local:16379", state.MasterAddress)
	for _, addr := range router.dialedAddrs() {
		assert.True(t, strings.HasSuffix(addr, ":16379"),
			"every TLS probe must go to the TLS port, got %q", addr)
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	for i, used := range router.tlsUsed {
		assert.True(t, used, "probe %d must be handed the TLS config built from ca.crt", i)
	}
	assert.NotContains(t, capture.joined(), "Could not build TLS config",
		"a valid ca.crt must produce a usable TLS config")
}

// --- findMaster against pods that answer ---

func TestFindMaster_LiveResponses(t *testing.T) {
	tests := []struct {
		name    string
		serve   map[string]string
		wantPod string
		wantErr string
	}{
		{
			name:    "the sole master is returned even with no replicas attached",
			serve:   map[string]string{"test-1": masterInfo(0)},
			wantPod: "test-1",
		},
		{
			name:    "pods that refuse the connection are skipped",
			serve:   map[string]string{"test-2": masterInfo(1)},
			wantPod: "test-2",
		},
		{
			name:    "a role the operator does not know is not a master",
			serve:   map[string]string{"test-0": infoReply("role:sentinel")},
			wantErr: "no master found among 3 pods",
		},
		{
			name:    "a replica-only cluster has no master",
			serve:   map[string]string{"test-0": replicaInfo(), "test-1": replicaInfo()},
			wantErr: "no master found among 3 pods",
		},
		{
			name:    "an INFO payload without a role line yields no candidate",
			serve:   map[string]string{"test-0": respBulk("this is not an INFO payload")},
			wantErr: "no master found among 3 pods",
		},
		{
			name:    "an empty bulk reply yields no candidate",
			serve:   map[string]string{"test-0": "$-1\r\n"},
			wantErr: "no master found among 3 pods",
		},
		{
			name:    "a RESP error reply skips the pod",
			serve:   map[string]string{"test-0": "-ERR unknown command 'INFO'\r\n"},
			wantErr: "no master found among 3 pods",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newProbeContext(t)
			v := newTestValkey("test", "default")

			router := newProbeRouter()
			for pod, reply := range tc.serve {
				router.serve(t, pod, answers(reply))
			}
			checker := router.install(newFakeChecker(runningTestPods(3)...))

			pod, addr, err := checker.findMaster(ctx, v, "", nil)

			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				assert.Empty(t, pod)
				assert.Empty(t, addr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantPod, pod)
			assert.Equal(t, tc.wantPod+".test-headless.default.svc.cluster.local:6379", addr)
		})
	}
}

// Two pods claiming to be master is a split brain: the one the replicas are
// actually attached to holds the live data and must win, regardless of ordinal.
func TestFindMaster_SplitBrainPrefersTheMasterWithMostReplicas(t *testing.T) {
	ctx, capture := newProbeContext(t)
	v := newTestValkey("test", "default")

	router := newProbeRouter()
	router.serve(t, "test-0", answers(masterInfo(0)))
	router.serve(t, "test-2", answers(masterInfo(2)))
	checker := router.install(newFakeChecker(runningTestPods(3)...))

	pod, addr, err := checker.findMaster(ctx, v, "", nil)

	require.NoError(t, err)
	assert.Equal(t, "test-2", pod, "the lowest ordinal must not win over the pod serving the replicas")
	assert.Equal(t, "test-2.test-headless.default.svc.cluster.local:6379", addr)
	logged := capture.joined()
	assert.Contains(t, logged, "Multiple masters detected")
	assert.Contains(t, logged, "test-0")
	assert.Contains(t, logged, "test-2")
}

// --- checkSentinel quorum ---

func TestCheckSentinel_AgreementIsAMajority(t *testing.T) {
	tests := []struct {
		name  string
		serve map[string]string
		want  bool
	}{
		{
			name: "all three sentinels agree",
			serve: map[string]string{
				"test-sentinel-0": sentinelMasterReply("master"),
				"test-sentinel-1": sentinelMasterReply("master"),
				"test-sentinel-2": sentinelMasterReply("master"),
			},
			want: true,
		},
		{
			name: "two of three is still a majority",
			serve: map[string]string{
				"test-sentinel-0": sentinelMasterReply("master"),
				"test-sentinel-1": sentinelMasterReply("master"),
			},
			want: true,
		},
		{
			name: "one agreeing sentinel is not a majority",
			serve: map[string]string{
				"test-sentinel-0": sentinelMasterReply("master"),
			},
			want: false,
		},
		{
			name: "a sentinel flagging the master as down does not agree",
			serve: map[string]string{
				"test-sentinel-0": sentinelMasterReply("master"),
				"test-sentinel-1": sentinelMasterReply("s_down,master"),
				"test-sentinel-2": sentinelMasterReply("o_down,master"),
			},
			want: false,
		},
		{
			name:  "no sentinel answers",
			serve: map[string]string{},
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newProbeContext(t)
			v := newTestValkey("test", "default")

			router := newProbeRouter()
			for pod, reply := range tc.serve {
				router.serve(t, pod, answers(reply))
			}

			agreed := router.install(newFakeChecker()).checkSentinel(ctx, v)

			assert.Equal(t, tc.want, agreed)
			assert.Equal(t, []string{
				"test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379",
				"test-sentinel-1.test-sentinel-headless.default.svc.cluster.local:26379",
				"test-sentinel-2.test-sentinel-headless.default.svc.cluster.local:26379",
			}, router.dialedAddrs(), "every sentinel replica is asked once")
		})
	}
}

// The sentinel view is only consulted once a master is known, and it lands in
// the state the controller reads.
func TestCheckCluster_SentinelAgreementReachesTheState(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default")

	router := newProbeRouter()
	router.serve(t, "test-0", answers(masterInfo(2)))
	for i := 0; i < 3; i++ {
		router.serve(t, fmt.Sprintf("test-sentinel-%d", i), answers(sentinelMasterReply("master")))
	}

	state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)

	require.NoError(t, state.Error)
	assert.Equal(t, "test-0", state.MasterPod)
	assert.True(t, state.SentinelMonitoring)
	assert.True(t, state.AllSynced)
}

// A Checker that leaves the factory nil must keep building its own clients, so
// production behaviour is unchanged by the seam.
func TestNewValkeyClient_FactoryIsOptional(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel)

	checker := newFakeChecker(runningTestPods(1)...)
	require.Nil(t, checker.NewValkeyClientFn)

	state := checker.CheckCluster(ctx, v)

	require.Error(t, state.Error, "without the seam the probes leave for pod FQDNs that do not resolve")
	assert.Contains(t, state.Error.Error(), "no master found")
	assert.Equal(t, []string{"test-0.test-headless.default.svc.cluster.local"}, resolverProbe.hosts(),
		"the pod FQDN, not a loopback address, is what a nil factory dials")
}
