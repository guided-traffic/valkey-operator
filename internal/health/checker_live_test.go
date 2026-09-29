package health

import (
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strconv"
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

// dialedAddrsSorted returns the same multiset sorted, for the assertions that
// have to survive findMaster probing its pods concurrently: the count per address
// still carries the meaning (every pod is dialled once, the master included), the
// arrival order no longer does.
func (r *probeRouter) dialedAddrsSorted() []string {
	addrs := r.dialedAddrs()
	sort.Strings(addrs)
	return addrs
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

// masterInfo is the INFO replication payload of a master reporting
// connected_slaves:n. That count includes a replica from its sync request on,
// while it still holds nothing, and a master's reply never carries
// master_sync_in_progress, so nothing in it says whether a replica is synced.
func masterInfo(connectedSlaves int) string {
	return infoReply("role:master", fmt.Sprintf("connected_slaves:%d", connectedSlaves))
}

// replicaInfo is the INFO replication payload of a replica synced to a master.
func replicaInfo() string {
	return infoReply("role:slave", "master_link_status:up", "connected_slaves:0")
}

// replicaInFullSync is the payload of a replica receiving its master's dataset:
// the link is down for the whole transfer and master_sync_in_progress is 1
// (measured on Valkey 9.1.1 and 8.1.9). The pod holds nothing yet.
func replicaInFullSync() string {
	return infoReply("role:slave", "master_link_status:down", "master_sync_in_progress:1", "connected_slaves:0")
}

// replicaConnecting is the payload of a replica right after REPLICAOF: the link is
// not up and no transfer has started, so master_sync_in_progress is still 0.
func replicaConnecting() string {
	return infoReply("role:slave", "master_link_status:down", "master_sync_in_progress:0", "connected_slaves:0")
}

// sentinelMasterReply is the SENTINEL MASTER response of a Sentinel that agrees
// on the master.
func sentinelMasterReply(flags string) string {
	return sentinelMasterReplyWithPeers(flags, 2)
}

// sentinelMasterReplyWithPeers is the same reply with num-other-sentinels set,
// which is what peer-table drift looks like on the wire.
func sentinelMasterReplyWithPeers(flags string, otherSentinels int) string {
	return respArray("name", "mymaster", "ip", "10.0.0.1", "port", "6379",
		"flags", flags, "num-slaves", "2", "quorum", "2",
		"num-other-sentinels", strconv.Itoa(otherSentinels))
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
// one, which is what a pod that is killed right after answering looks like.
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
	assert.Equal(t, int32(2), state.ReadyReplicas,
		"the two replicas prove their sync in their own replies, not in the master's connected_slaves")
	assert.True(t, state.AllSynced)
	assert.False(t, state.SentinelMonitoring, "sentinel is disabled, so it is never consulted")
	assert.Equal(t, []string{
		"test-0.test-headless.default.svc.cluster.local:6379",
		"test-1.test-headless.default.svc.cluster.local:6379",
		"test-2.test-headless.default.svc.cluster.local:6379",
	}, router.dialedAddrsSorted(),
		"every pod is dialled once: the replicas are judged on the replies findMaster collected, "+
			"and the master is not asked a second time")
}

// TestCheckCluster_ReplicaAccounting pins where "synced" is read: in each
// replica's own INFO replication, never in the master's
// (docs/adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md, D2).
// The master replies below are the shape Valkey gives -- connected_slaves already
// counts a replica that is still receiving the dataset -- and none of them decides
// the count. An empty replica reply is a pod that does not answer.
func TestCheckCluster_ReplicaAccounting(t *testing.T) {
	tests := []struct {
		name              string
		master            string
		replicas          [2]string // test-1, test-2
		wantReadyReplicas int32
		wantAllSynced     bool
	}{
		{
			name:              "both replicas synced",
			master:            masterInfo(2),
			replicas:          [2]string{replicaInfo(), replicaInfo()},
			wantReadyReplicas: 2,
			wantAllSynced:     true,
		},
		{
			// The case the master-side count got wrong: the master reports
			// connected_slaves:2 and no master_sync_in_progress while test-2 is
			// still receiving the dataset.
			name:              "a replica in full sync is not synced though the master counts it",
			master:            masterInfo(2),
			replicas:          [2]string{replicaInfo(), replicaInFullSync()},
			wantReadyReplicas: 1,
			wantAllSynced:     false,
		},
		{
			name:              "a replica still connecting is not synced",
			master:            masterInfo(1),
			replicas:          [2]string{replicaInfo(), replicaConnecting()},
			wantReadyReplicas: 1,
			wantAllSynced:     false,
		},
		{
			name:              "a replica that does not answer is not counted",
			master:            masterInfo(2),
			replicas:          [2]string{replicaInfo(), ""},
			wantReadyReplicas: 1,
			wantAllSynced:     false,
		},
		{
			// Replaces the old clamp case: a master count above the spec used to be
			// cut down to it and read as every replica synced.
			name:              "a master counting more replicas than the spec expects does not raise the count",
			master:            masterInfo(7),
			replicas:          [2]string{replicaInfo(), replicaInFullSync()},
			wantReadyReplicas: 1,
			wantAllSynced:     false,
		},
		{
			name:              "a second master is not a synced replica",
			master:            masterInfo(1),
			replicas:          [2]string{replicaInfo(), masterInfo(0)},
			wantReadyReplicas: 1,
			wantAllSynced:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newProbeContext(t)
			v := newTestValkey("test", "default", noSentinel)

			router := newProbeRouter()
			router.serve(t, "test-0", answers(tc.master))
			for i, reply := range tc.replicas {
				if reply != "" {
					router.serve(t, fmt.Sprintf("test-%d", i+1), answers(reply))
				}
			}

			state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)

			require.NoError(t, state.Error)
			assert.Equal(t, "test-0", state.MasterPod)
			assert.Equal(t, tc.wantReadyReplicas, state.ReadyReplicas)
			assert.Equal(t, tc.wantAllSynced, state.AllSynced)
		})
	}
}

// TestCheckCluster_ReplicasInFullSyncAreNotSyncedThoughTheMasterCountsThem is the
// case the master-side count reported as healthy. While both replicas receive the
// dataset the master already counts them in connected_slaves, and its reply
// carries no master_sync_in_progress -- that field exists only in a replica's
// reply, where a full sync shows as master_link_status:down with
// master_sync_in_progress:1 (measured on Valkey 9.1.1 and 8.1.9). Read from the
// master, this was AllSynced with two replicas that held nothing.
func TestCheckCluster_ReplicasInFullSyncAreNotSyncedThoughTheMasterCountsThem(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel)

	router := newProbeRouter()
	router.serve(t, "test-0", answers(masterInfo(2)))
	router.serve(t, "test-1", answers(replicaInFullSync()))
	router.serve(t, "test-2", answers(replicaInFullSync()))

	state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)

	require.NoError(t, state.Error)
	assert.Equal(t, "test-0", state.MasterPod)
	assert.Equal(t, int32(0), state.ReadyReplicas, "a replica in full sync holds no dataset yet")
	assert.False(t, state.AllSynced, "connected_slaves:2 counts both replicas from their sync request on")
}

// TestCheckCluster_ReplicasThatDoNotAnswerAreNotSynced replaces the test of the
// second master probe, which ADR 0037 D2 removed: CheckCluster used to dial the
// master again for its INFO replication, count from its connected_slaves, and fail
// the check when that second dial failed. The master is now asked once, in
// findMaster, and nothing in its reply is counted, so a master that hangs up
// after its first answer is no error, and a master whose replicas stay silent
// reports no synced replica whatever its connected_slaves says.
func TestCheckCluster_ReplicasThatDoNotAnswerAreNotSynced(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", noSentinel)

	router := newProbeRouter()
	router.serve(t, "test-0", diesAfterFirstAnswer(masterInfo(2)))

	state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)

	require.NoError(t, state.Error, "the master is asked once; there is no second probe left for it to fail")
	assert.Equal(t, "test-0", state.MasterPod)
	assert.Zero(t, state.ReadyReplicas, "a silent pod proves nothing, and the master's count is not read")
	assert.False(t, state.AllSynced)
}

// The password from the auth Secret has to reach every probe findMaster sends,
// to the master and the replicas alike.
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
		// wantRoles is the Role of every ordinal whose reply findMaster must hand
		// back at that ordinal; every other slot must be nil.
		wantRoles map[int]string
	}{
		{
			name:      "the sole master is returned even with no replicas attached",
			serve:     map[string]string{"test-1": masterInfo(0)},
			wantPod:   "test-1",
			wantRoles: map[int]string{1: "master"},
		},
		{
			name:      "pods that refuse the connection are skipped",
			serve:     map[string]string{"test-2": masterInfo(1)},
			wantPod:   "test-2",
			wantRoles: map[int]string{2: "master"},
		},
		{
			name:      "a role the operator does not know is not a master",
			serve:     map[string]string{"test-0": infoReply("role:sentinel")},
			wantErr:   "no master found among 3 pods",
			wantRoles: map[int]string{0: "sentinel"},
		},
		{
			name:      "a replica-only cluster has no master",
			serve:     map[string]string{"test-0": replicaInfo(), "test-1": replicaInfo()},
			wantErr:   "no master found among 3 pods",
			wantRoles: map[int]string{0: "slave", 1: "slave"},
		},
		{
			name:      "an INFO payload without a role line yields no candidate",
			serve:     map[string]string{"test-0": respBulk("this is not an INFO payload")},
			wantErr:   "no master found among 3 pods",
			wantRoles: map[int]string{0: ""},
		},
		{
			name:      "an empty bulk reply yields no candidate",
			serve:     map[string]string{"test-0": "$-1\r\n"},
			wantErr:   "no master found among 3 pods",
			wantRoles: map[int]string{0: ""},
		},
		{
			name:      "a RESP error reply skips the pod",
			serve:     map[string]string{"test-0": "-ERR unknown command 'INFO'\r\n"},
			wantErr:   "no master found among 3 pods",
			wantRoles: map[int]string{},
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

			pod, addr, replies, err := checker.findMaster(ctx, v, "", nil)

			// The replies come back on success and on failure alike, one slot per
			// ordinal: CheckCluster judges the replicas on them without a second dial.
			require.Len(t, replies, 3, "one slot per ordinal, answered or not")
			for i, info := range replies {
				role, answered := tc.wantRoles[i]
				if !answered {
					assert.Nil(t, info, "test-%d gave no reply, so its slot stays nil", i)
					continue
				}
				if assert.NotNil(t, info, "test-%d answered, so its reply sits at its ordinal", i) {
					assert.Equal(t, role, info.Role, "test-%d", i)
				}
			}

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

	pod, addr, _, err := checker.findMaster(ctx, v, "", nil)

	require.NoError(t, err)
	assert.Equal(t, "test-2", pod, "the lowest ordinal must not win over the pod serving the replicas")
	assert.Equal(t, "test-2.test-headless.default.svc.cluster.local:6379", addr)
	logged := capture.joined()
	assert.Contains(t, logged, "Multiple masters detected")
	assert.Contains(t, logged, "test-0")
	assert.Contains(t, logged, "test-2")
}

// --- sentinel peer tables ---

// The peer count rides along on the reply the quorum check already asked for.
// Collecting it must not cost a second connection per sentinel per pass.
func TestObserveSentinels_CollectsPeerCountsWithoutExtraDials(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default")

	router := newProbeRouter()
	router.serve(t, "test-sentinel-0", answers(sentinelMasterReplyWithPeers("master", 4)))
	router.serve(t, "test-sentinel-1", answers(sentinelMasterReplyWithPeers("master", 3)))
	router.serve(t, "test-sentinel-2", answers(sentinelMasterReplyWithPeers("master", 2)))

	observed := router.install(newFakeChecker()).observeSentinels(ctx, v)

	assert.Equal(t, map[string]int{
		"test-sentinel-0": 4,
		"test-sentinel-1": 3,
		"test-sentinel-2": 2,
	}, observed.peers)
	assert.Equal(t, 2, observed.expectedPeers, "three sentinels means two others each")
	assert.True(t, observed.monitoring())
	assert.Len(t, router.dialedAddrs(), 3, "one dial per sentinel, peer counts included")
}

// A sentinel that does not answer is absent from the map rather than recorded as
// knowing nobody: zero would read as the cleanest table in the cluster and hide
// the drift on the ones that did answer.
func TestObserveSentinels_SilentSentinelIsAbsentNotZero(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default")

	router := newProbeRouter()
	router.serve(t, "test-sentinel-0", answers(sentinelMasterReplyWithPeers("master", 4)))

	observed := router.install(newFakeChecker()).observeSentinels(ctx, v)

	assert.Equal(t, map[string]int{"test-sentinel-0": 4}, observed.peers)
}

// CheckCluster is the only caller in production, so the counts have to survive
// the trip into ClusterState -- the condition is written from there, not from the
// observation.
func TestCheckCluster_CarriesSentinelPeerCounts(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default")

	router := newProbeRouter()
	router.serve(t, "test-0", answers(masterInfo(2)))
	router.serve(t, "test-1", answers(replicaInfo()))
	router.serve(t, "test-2", answers(replicaInfo()))
	for i, peers := range []int{4, 3, 2} {
		router.serve(t, fmt.Sprintf("test-sentinel-%d", i),
			answers(sentinelMasterReplyWithPeers("master", peers)))
	}

	state := router.install(newFakeChecker(runningTestPods(3)...)).CheckCluster(ctx, v)
	require.NoError(t, state.Error)

	assert.Equal(t, 2, state.SentinelPeersExpected)
	assert.Equal(t, 4, state.SentinelPeers["test-sentinel-0"])
	assert.Equal(t, 2, state.SentinelPeers["test-sentinel-2"])
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

			agreed := router.install(newFakeChecker()).observeSentinels(ctx, v).monitoring()

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
	router.serve(t, "test-1", answers(replicaInfo()))
	router.serve(t, "test-2", answers(replicaInfo()))
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
