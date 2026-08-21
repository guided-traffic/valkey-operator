package controller

import (
	"bufio"
	"context"
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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The Sentinel half of the rolling update is a protocol, not a sequence of helper
// calls: every state decides which pod to talk to, which command to send there and
// what the answer means for the next state. Asserting that a mock method was called
// says nothing about any of that, so the tests in this file put a real RESP listener
// behind every address the operator dials and assert on the resulting command log --
// the command, its arguments, and the endpoint it landed on -- plus the annotations
// the pass persisted and the pods it deleted.

// --- RESP router ------------------------------------------------------------

// dialedCommand is one command the operator sent, together with the address it
// meant to reach. The target matters as much as the command: SENTINEL FAILOVER on
// a data pod and WAIT on a sentinel are both "a command was sent".
type dialedCommand struct {
	target string
	cmd    string
}

// respRouter gives every address the operator dials its own RESP listener and
// records what arrived there. The reply function receives the target, so a test
// can make one sentinel answer differently from its peers.
type respRouter struct {
	t     *testing.T
	reply func(target string, args []string) string

	mu        sync.Mutex
	log       []dialedCommand
	listeners map[string]string // operator address -> real listener address
}

func newRESPRouter(t *testing.T, reply func(target string, args []string) string) *respRouter {
	t.Helper()
	return &respRouter{t: t, reply: reply, listeners: make(map[string]string)}
}

// attach routes every Valkey client the reconciler builds into this router.
func (rr *respRouter) attach(r *ValkeyReconciler) {
	r.NewValkeyClientFn = func(addr, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(rr.listenerFor(addr))
	}
}

func (rr *respRouter) listenerFor(target string) string {
	if addr, ok := rr.listeners[target]; ok {
		return addr
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(rr.t, err)
	rr.t.Cleanup(func() { _ = ln.Close() })
	go rr.serve(ln, target)

	rr.listeners[target] = ln.Addr().String()
	return ln.Addr().String()
}

func (rr *respRouter) serve(ln net.Listener, target string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			reader := bufio.NewReader(conn)
			for {
				args, readErr := readRESPCommand(reader)
				if readErr != nil {
					return
				}
				rr.mu.Lock()
				rr.log = append(rr.log, dialedCommand{target: target, cmd: strings.Join(args, " ")})
				rr.mu.Unlock()

				answer := rr.reply(target, args)
				if answer == "" {
					return // models an endpoint that drops the connection
				}
				if _, writeErr := conn.Write([]byte(answer)); writeErr != nil {
					return
				}
			}
		}()
	}
}

// sent returns every command the operator issued, in order, without its target.
func (rr *respRouter) sent() []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	out := make([]string, 0, len(rr.log))
	for _, entry := range rr.log {
		out = append(out, entry.cmd)
	}
	return out
}

// commandsTo returns the commands that reached one endpoint, in order.
func (rr *respRouter) commandsTo(target string) []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	out := []string{}
	for _, entry := range rr.log {
		if entry.target == target {
			out = append(out, entry.cmd)
		}
	}
	return out
}

// targetsFor returns the endpoints that received a command starting with prefix.
func (rr *respRouter) targetsFor(prefix string) []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	out := []string{}
	for _, entry := range rr.log {
		if strings.HasPrefix(entry.cmd, prefix) {
			out = append(out, entry.target)
		}
	}
	return out
}

// readRESPCommand reads one RESP array of bulk strings, the shape valkeyclient
// sends for every command.
func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	header = strings.TrimRight(header, "\r\n")
	if !strings.HasPrefix(header, "*") {
		return nil, fmt.Errorf("unexpected request header %q", header)
	}
	count, err := strconv.Atoi(header[1:])
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		bulk, bulkErr := reader.ReadString('\n')
		if bulkErr != nil {
			return nil, bulkErr
		}
		bulk = strings.TrimRight(bulk, "\r\n")
		if !strings.HasPrefix(bulk, "$") {
			return nil, fmt.Errorf("unexpected bulk header %q", bulk)
		}
		size, sizeErr := strconv.Atoi(bulk[1:])
		if sizeErr != nil {
			return nil, sizeErr
		}
		buf := make([]byte, size+2) // payload plus CRLF
		if _, readErr := io.ReadFull(reader, buf); readErr != nil {
			return nil, readErr
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

// --- RESP replies -----------------------------------------------------------

const respOK = "+OK\r\n"

func respInt(n int) string { return fmt.Sprintf(":%d\r\n", n) }

func respErr(msg string) string { return "-ERR " + msg + "\r\n" }

func respBulkArray(items ...string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d\r\n", len(items))
	for _, item := range items {
		fmt.Fprintf(&sb, "$%d\r\n%s\r\n", len(item), item)
	}
	return sb.String()
}

// respSentinelMaster is the flat key/value array SENTINEL MASTER answers with.
func respSentinelMaster(numSlaves int) string {
	return respBulkArray(
		"name", "mymaster",
		"flags", "master",
		"num-slaves", strconv.Itoa(numSlaves),
		"quorum", "2",
	)
}

// healthyCluster answers the commands a Sentinel rolling update issues: SENTINEL
// MASTER reports numSlaves discovered replicas, DBSIZE a non-empty keyspace, WAIT
// acknowledges every replica it was asked about, and everything else is +OK.
func healthyCluster(numSlaves int) func(string, []string) string {
	return func(_ string, args []string) string {
		return clusterAnswer(numSlaves, args)
	}
}

func clusterAnswer(numSlaves int, args []string) string {
	if len(args) == 0 {
		return respOK
	}
	switch strings.ToUpper(args[0]) {
	case "SENTINEL":
		if len(args) >= 2 && strings.EqualFold(args[1], "MASTER") {
			return respSentinelMaster(numSlaves)
		}
		return respOK
	case "DBSIZE":
		return respInt(4711)
	case "WAIT":
		if len(args) >= 2 {
			return respInt(atoiOrZero(args[1]))
		}
		return respInt(0)
	default:
		return respOK
	}
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// --- Cluster fixture --------------------------------------------------------

const (
	oldValkeyImage = "valkey/valkey:8.0"
	newValkeyImage = "valkey/valkey:9.0"
)

// sentinelClusterCR builds a Sentinel-enabled Valkey CR with replicas data pods and
// the same number of sentinels.
func sentinelClusterCR(name string, replicas int32, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	return newTestValkey(name, testNamespace, func(v *vkov1.Valkey) {
		v.Spec.Replicas = replicas
		v.Spec.Image = newValkeyImage
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: replicas}
		for _, opt := range opts {
			opt(v)
		}
	})
}

// midFailoverCluster is the shape the Sentinel rolling update is in when it reaches
// the master failover: three pods, pod-0 is the master and still runs the old image,
// pod-1 and pod-2 have already been replaced and are ready.
//
// It returns the reconciler, its client, the CR as persisted (so the annotation
// writes of the state machine carry a resourceVersion) and the pod states the
// failover functions are called with.
func midFailoverCluster(t *testing.T, name string, annotations map[string]string,
	funcs *interceptor.Funcs) (*ValkeyReconciler, client.Client, *vkov1.Valkey, []podState) {
	t.Helper()

	v := sentinelClusterCR(name, 3)
	v.Annotations = annotations

	pod0 := createPodForSts(v, 0, oldValkeyImage, true)
	pod1 := createPodForSts(v, 1, newValkeyImage, true)
	pod2 := createPodForSts(v, 2, newValkeyImage, true)

	objs := []client.Object{v, pod0, pod1, pod2}

	var r *ValkeyReconciler
	var c client.Client
	if funcs != nil {
		r, c = newInterceptedReconciler(*funcs, objs...)
	} else {
		r, c = newTestReconciler(objs...)
	}

	pods := []podState{
		{name: pod0.Name, pod: pod0, needsUpdate: true, isMaster: true, ready: true, exists: true},
		{name: pod1.Name, pod: pod1, ready: true, exists: true},
		{name: pod2.Name, pod: pod2, ready: true, exists: true},
	}

	return r, c, crGet(t, c, name), pods
}

// dataAddr is the address the operator dials for a data pod.
func dataAddr(v *vkov1.Valkey, ordinal int) string {
	podName := fmt.Sprintf("%s-%d", common.StatefulSetName(v, common.ComponentValkey), ordinal)
	return health.PodAddressForComponent(v, podName, common.ComponentValkey, int(builder.ServicePort(v)))
}

// sentinelAddr is the address the operator dials for a sentinel pod.
func sentinelAddr(v *vkov1.Valkey, ordinal, port int) string {
	podName := fmt.Sprintf("%s-%d", common.StatefulSetName(v, common.ComponentSentinel), ordinal)
	return health.PodAddressForComponent(v, podName, common.ComponentSentinel, port)
}

// podFQDN is the headless-service name of a data pod, the form the operator hands
// to SENTINEL MONITOR and REPLICAOF.
func podFQDN(v *vkov1.Valkey, ordinal int) string {
	return fmt.Sprintf("%s-%d.%s.%s.svc.cluster.local",
		common.StatefulSetName(v, common.ComponentValkey), ordinal,
		common.HeadlessServiceName(v, common.ComponentValkey),
		v.Namespace)
}

// masterInfo is a healthy master with the given number of attached replicas.
func masterInfo(connectedSlaves int) *valkeyclient.ReplicationInfo {
	return &valkeyclient.ReplicationInfo{Role: common.RoleMaster, ConnectedSlaves: connectedSlaves}
}

// replicaInfo is a replica whose link to its master is up and not resyncing.
func replicaInfo() *valkeyclient.ReplicationInfo {
	return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}
}

// failCRUpdateFrom rejects every Valkey CR update from the nth one on (1-based), so
// a test can break exactly one write of a multi-write path. Status writes go through
// the sub-resource interceptor and are unaffected.
func failCRUpdateFrom(n int, seen *int) interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, isCR := obj.(*vkov1.Valkey); isCR {
				*seen++
				if *seen >= n {
					return apierrors.NewInternalError(fmt.Errorf("cr update rejected"))
				}
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
}

// failOnlyCRUpdate rejects exactly the nth Valkey CR update (1-based) and lets
// every other one through. failCRUpdateFrom cannot pin a write-ordering
// guarantee on its own: when it breaks the first write it breaks the second one
// too, so a pass that ignored the first error would still fail on the second and
// look identical from the outside.
func failOnlyCRUpdate(n int, seen *int) interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, isCR := obj.(*vkov1.Valkey); isCR {
				*seen++
				if *seen == n {
					return apierrors.NewInternalError(fmt.Errorf("cr update %d rejected", n))
				}
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
}

func rfc3339Ago(d time.Duration) string {
	return time.Now().Add(-d).UTC().Format(time.RFC3339)
}

func mustParseRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err, "annotation %q must be an RFC3339 timestamp", s)
	return ts
}

// --- isSentinelAwareOfReplicas ---------------------------------------------

func TestIsSentinelAwareOfReplicas_TrueWhenSentinelKnowsEveryReplica(t *testing.T) {
	v := sentinelClusterCR("aware", 3)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	assert.True(t, r.isSentinelAwareOfReplicas(context.Background(), v, 2))
	assert.Equal(t, []string{"SENTINEL MASTER aware"}, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		"the first reachable sentinel answers the question")
	assert.Empty(t, router.commandsTo(sentinelAddr(v, 1, builder.SentinelPort)),
		"no further sentinel is polled once one confirms")
}

// The first reachable sentinel decides, and it decides for the whole set: a
// sentinel that is still discovering blocks the failover even when its peers
// already know every replica.
//
// This contradicts the doc comment on isSentinelAwareOfReplicas ("Returns true if
// at least one sentinel reports enough replicas") -- the loop returns false at the
// first under-informed answer instead of trying the next sentinel. The test pins
// the behaviour that ships; see the finding reported with this change.
func TestIsSentinelAwareOfReplicas_FirstReachableSentinelDecidesForAll(t *testing.T) {
	v := sentinelClusterCR("aware-first", 3)
	r, _ := newTestReconciler(v)

	lagging := sentinelAddr(v, 0, builder.SentinelPort)
	router := newRESPRouter(t, func(target string, args []string) string {
		if target == lagging {
			return clusterAnswer(0, args)
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	assert.False(t, r.isSentinelAwareOfReplicas(context.Background(), v, 2),
		"one lagging sentinel is enough to hold the failover back")
	assert.Empty(t, router.commandsTo(sentinelAddr(v, 1, builder.SentinelPort)),
		"the peers that do know every replica are never asked")
}

// An unreachable sentinel is skipped, not treated as an answer.
func TestIsSentinelAwareOfReplicas_SkipsUnreachableSentinels(t *testing.T) {
	v := sentinelClusterCR("aware-skip", 3)
	r, _ := newTestReconciler(v)

	dead := sentinelAddr(v, 0, builder.SentinelPort)
	router := newRESPRouter(t, func(target string, args []string) string {
		if target == dead {
			return "" // drop the connection
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	assert.True(t, r.isSentinelAwareOfReplicas(context.Background(), v, 2))
	assert.Equal(t, []string{"SENTINEL MASTER aware-skip"}, router.commandsTo(sentinelAddr(v, 1, builder.SentinelPort)),
		"the next sentinel has to be asked after an unreachable one")
}

// --- handleMasterFailover ---------------------------------------------------

// Without a master there is no index to dereference and nothing to fail over.
func TestHandleMasterFailover_NoMasterIsANoOp(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-nomaster", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	assert.Nil(t, r.handleMasterFailover(context.Background(), v, pods, -1))
	assert.Empty(t, router.sent(), "a pass without a master must not command the cluster")
	assert.Empty(t, crGet(t, c, "hmf-nomaster").Annotations[annotationRollingUpdateState])
}

// A master that already runs the new image is not failed over: the rolling update
// is done with it.
func TestHandleMasterFailover_UpToDateMasterIsANoOp(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-current", nil, nil)
	pods[0].needsUpdate = false
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	assert.Nil(t, r.handleMasterFailover(context.Background(), v, pods, 0))
	assert.Empty(t, router.sent())
	assert.Empty(t, crGet(t, c, "hmf-current").Annotations[annotationRollingUpdateState])
}

// A second reconcile arriving during the same storm must not trigger a second
// failover, and must not push out the deadline of the one in flight.
func TestHandleMasterFailover_SkipsWhenAFailoverIsAlreadyInFlight(t *testing.T) {
	for _, state := range []string{stateFailoverTriggered, stateReplacingMaster, stateFailoverReset} {
		t.Run(state, func(t *testing.T) {
			armed := rfc3339Ago(5 * time.Second)
			r, c, v, pods := midFailoverCluster(t, "hmf-inflight", map[string]string{
				annotationRollingUpdateState: state,
				annotationFailoverTimestamp:  armed,
			}, nil)
			router := newRESPRouter(t, healthyCluster(2))
			router.attach(r)
			// Everything behind the guard is healthy on purpose: without the
			// state check the pass would run all the way to SENTINEL FAILOVER.
			// With an unreachable checker it would stall in waitForReplicasReady
			// and produce the same requeue, and the guard would go untested.
			r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
				"hmf-inflight-1": replicaInfo(),
				"hmf-inflight-2": replicaInfo(),
			}}

			result := r.handleMasterFailover(context.Background(), v, pods, 0)

			require.NotNil(t, result)
			assert.True(t, result.NeedsRequeue)
			assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
			assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"),
				"the failover in flight must not be triggered a second time")
			assert.Equal(t, armed, crGet(t, c, "hmf-inflight").Annotations[annotationFailoverTimestamp],
				"re-stamping the timestamp would keep resetting the stale-failover deadline")
		})
	}
}

// The failover is only safe once every replica is ready on the new image: a replica
// that is still coming up cannot take over the writes.
func TestHandleMasterFailover_WaitsForAReplicaThatIsNotReady(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-notready", nil, nil)
	pods[2].ready = false
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-notready-1": replicaInfo(),
		"hmf-notready-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent(), "no WAIT and no failover while a replica is missing")
	assert.Empty(t, crGet(t, c, "hmf-notready").Annotations[annotationRollingUpdateState])
}

// A replica that is still pulling the dataset would lose the writes it has not
// received yet, so the failover waits for the sync to finish.
func TestHandleMasterFailover_WaitsWhileAReplicaIsStillSyncing(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-syncing", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-syncing-1": {Role: "slave", MasterSyncInProgress: true},
		"hmf-syncing-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent())
	assert.Empty(t, crGet(t, c, "hmf-syncing").Annotations[annotationRollingUpdateState])
}

// An unreadable replication state is not an implicit "synced".
func TestHandleMasterFailover_WaitsWhenReplicationStateIsUnreadable(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-unknown", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-unknown-1": replicaInfo(),
		// pod-2 is missing from the table, so GetReplicationInfo fails for it.
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, crGet(t, c, "hmf-unknown").Annotations[annotationRollingUpdateState])
}

// WAIT is the last barrier against losing acknowledged writes on the promotion. If
// the master cannot confirm the replicas caught up, no failover is triggered.
func TestHandleMasterFailover_DoesNotFailOverWhenWriteSyncFails(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-wait", nil, nil)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) > 0 && strings.EqualFold(args[0], "WAIT") {
			return respErr("NOREPLICAS not enough good replicas")
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-wait-1": replicaInfo(),
		"hmf-wait-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Equal(t, []string{dataAddr(v, 0)}, router.targetsFor("WAIT"),
		"WAIT is sent to the master, not to a replica")
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"),
		"unacknowledged writes must block the promotion")
	assert.Empty(t, crGet(t, c, "hmf-wait").Annotations[annotationRollingUpdateState])
}

// The happy path: replicas ready and synced, writes acknowledged, sentinel aware --
// the state is recorded before the command goes out, so a concurrent reconcile
// cannot trigger a second failover.
func TestHandleMasterFailover_TriggersFailoverOnceEverythingIsSynced(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-go", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-go-1": replicaInfo(),
		"hmf-go-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, 15*time.Second, result.RequeueAfter)

	assert.Contains(t, router.sent(), fmt.Sprintf("WAIT 2 %d", waitWriteSyncTimeout),
		"both replaced replicas have to acknowledge the pending writes")
	assert.Equal(t, []string{dataAddr(v, 0)}, router.targetsFor("WAIT"))
	assert.Equal(t, []string{sentinelAddr(v, 0, builder.SentinelPort)}, router.targetsFor("SENTINEL FAILOVER"),
		"the failover is triggered on the first sentinel that accepts it")
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		"SENTINEL FAILOVER "+builder.SentinelMonitorName(v))

	stored := crGet(t, c, "hmf-go")
	assert.Equal(t, stateFailoverTriggered, stored.Annotations[annotationRollingUpdateState],
		"without the state a concurrent reconcile triggers a second failover")
	assert.WithinDuration(t, time.Now(),
		mustParseRFC3339(t, stored.Annotations[annotationFailoverTimestamp]), time.Minute,
		"the stale-failover deadline starts at the trigger")
	assert.Equal(t, vkov1.ValkeyPhaseFailover, stored.Status.Phase)
}

// Failing over before sentinel has discovered the replicas returns NOGOODSLAVE and
// burns the retry cycle, so the pass waits -- and arms the bound that caps the wait.
func TestHandleMasterFailover_WaitsForSentinelToDiscoverTheReplicas(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-blind", nil, nil)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-blind-1": replicaInfo(),
		"hmf-blind-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.Equal(t, 5*time.Second, result.RequeueAfter)
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"))

	stored := crGet(t, c, "hmf-blind")
	assert.Empty(t, stored.Annotations[annotationRollingUpdateState])
	assert.WithinDuration(t, time.Now(),
		mustParseRFC3339(t, stored.Annotations[annotationSentinelAwarenessStarted]), time.Minute,
		"the awareness wait has to be armed or it can never be declared stalled")
}

// The awareness wait is capped: after sentinelAwarenessTimeout the failover is
// triggered regardless and the NOGOODSLAVE retry cycle takes over, rather than
// stalling the rolling update before it ever attempted a promotion.
func TestHandleMasterFailover_ProceedsOnceSentinelAwarenessStalls(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-stalled", map[string]string{
		annotationSentinelAwarenessStarted: rfc3339Ago(sentinelAwarenessTimeout + time.Minute),
	}, nil)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-stalled-1": replicaInfo(),
		"hmf-stalled-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.Equal(t, 15*time.Second, result.RequeueAfter)
	assert.NotEmpty(t, router.targetsFor("SENTINEL FAILOVER"),
		"a stalled awareness wait must not block the promotion forever")
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hmf-stalled").Annotations[annotationRollingUpdateState])
}

// The state write comes first on purpose. If it fails, the pass fails -- triggering
// a failover the CR does not know about would leave the next pass replacing pods
// while sentinel is mid-promotion.
func TestHandleMasterFailover_SurfacesTheStateWriteFailure(t *testing.T) {
	writes := 0
	// Only the state write fails; the timestamp write behind it would succeed. A
	// pass that swallowed the state error would therefore reach the promotion,
	// which is exactly what this test has to catch.
	funcs := failOnlyCRUpdate(1, &writes)
	r, c, v, pods := midFailoverCluster(t, "hmf-statefail", nil, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-statefail-1": replicaInfo(),
		"hmf-statefail-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	require.Error(t, result.Error)
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"),
		"no failover may be triggered while its state cannot be recorded")
	assert.Empty(t, crGet(t, c, "hmf-statefail").Annotations[annotationRollingUpdateState])
}

// Same for the timestamp: it is the only thing that makes a hung failover
// retryable, so a failover is not triggered without it.
func TestHandleMasterFailover_SurfacesTheTimestampWriteFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(2, &writes)
	r, c, v, pods := midFailoverCluster(t, "hmf-tsfail", nil, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-tsfail-1": replicaInfo(),
		"hmf-tsfail-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	require.Error(t, result.Error)
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"))
	assert.Empty(t, crGet(t, c, "hmf-tsfail").Annotations[annotationFailoverTimestamp])
}

// A rejected SENTINEL FAILOVER is expected traffic (cooldown, NOGOODSLAVE): the
// state stays, every sentinel is tried, and handlePostFailover retries from there.
func TestHandleMasterFailover_RejectedFailoverCommandIsNotFatal(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmf-reject", nil, nil)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) >= 2 && strings.EqualFold(args[0], "SENTINEL") && strings.EqualFold(args[1], "FAILOVER") {
			return respErr("NOGOODSLAVE No suitable replica to promote")
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmf-reject-1": replicaInfo(),
		"hmf-reject-2": replicaInfo(),
	}}

	result := r.handleMasterFailover(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	require.NoError(t, result.Error, "a refused failover is retried, not surfaced as a failed pass")
	assert.Equal(t, 15*time.Second, result.RequeueAfter)
	assert.Len(t, router.targetsFor("SENTINEL FAILOVER"), 3, "every sentinel is tried before giving up")
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hmf-reject").Annotations[annotationRollingUpdateState],
		"the post-failover handler owns the retry from here")
}

// --- handleFailoverRetrigger ------------------------------------------------

// After a SENTINEL REMOVE+MONITOR sentinel needs to rediscover the replicas via INFO
// polling. Retriggering inside that window returns NOGOODSLAVE.
func TestHandleFailoverRetrigger_WaitsOutTheRediscoveryWindow(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hfr-young", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  rfc3339Ago(time.Second),
	}, nil)
	_ = pods
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)
	assert.Empty(t, router.sent(), "sentinel is not even polled inside the minimum wait")
	assert.Equal(t, stateFailoverReset, crGet(t, c, "hfr-young").Annotations[annotationRollingUpdateState])
}

// The minimum wait is a floor, not a guarantee: sentinel is asked whether it has
// actually rediscovered the replicas before the retrigger.
func TestHandleFailoverRetrigger_WaitsUntilSentinelHasRediscoveredReplicas(t *testing.T) {
	r, c, v, _ := midFailoverCluster(t, "hfr-blind", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  rfc3339Ago(failoverResetMinWait + time.Second),
	}, nil)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	assert.Equal(t, 5*time.Second, result.RequeueAfter)
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"))

	stored := crGet(t, c, "hfr-blind")
	assert.Equal(t, stateFailoverReset, stored.Annotations[annotationRollingUpdateState])
	assert.NotEmpty(t, stored.Annotations[annotationSentinelAwarenessStarted],
		"the retrigger path arms the same awareness bound as the first attempt")
}

// And the same cap applies here, for the same reason.
func TestHandleFailoverRetrigger_ProceedsOnceSentinelAwarenessStalls(t *testing.T) {
	r, c, v, _ := midFailoverCluster(t, "hfr-stalled", map[string]string{
		annotationRollingUpdateState:       stateFailoverReset,
		annotationFailoverTimestamp:        rfc3339Ago(failoverResetMinWait + time.Second),
		annotationSentinelAwarenessStarted: rfc3339Ago(sentinelAwarenessTimeout + time.Minute),
	}, nil)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	assert.Equal(t, 15*time.Second, result.RequeueAfter)
	assert.NotEmpty(t, router.targetsFor("SENTINEL FAILOVER"))
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hfr-stalled").Annotations[annotationRollingUpdateState])
}

// The retrigger restarts the stale-failover clock: the new attempt gets a full
// failoverRetryTimeout, not the remainder of the previous one.
func TestHandleFailoverRetrigger_RetriggersAndRestartsTheDeadline(t *testing.T) {
	before := rfc3339Ago(failoverResetMinWait + time.Minute)
	r, c, v, _ := midFailoverCluster(t, "hfr-go", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  before,
	}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	require.NoError(t, result.Error)
	assert.Equal(t, 15*time.Second, result.RequeueAfter)
	assert.Equal(t, []string{sentinelAddr(v, 0, builder.SentinelPort)}, router.targetsFor("SENTINEL FAILOVER"))

	stored := crGet(t, c, "hfr-go")
	assert.Equal(t, stateFailoverTriggered, stored.Annotations[annotationRollingUpdateState])
	assert.True(t,
		mustParseRFC3339(t, stored.Annotations[annotationFailoverTimestamp]).After(mustParseRFC3339(t, before)),
		"the retry needs its own timeout budget")
}

func TestHandleFailoverRetrigger_SurfacesTheStateWriteFailure(t *testing.T) {
	writes := 0
	// Only the state write fails -- see failOnlyCRUpdate: with every later write
	// broken too, ignoring this error would still surface an error from the next
	// one and the test could not tell the two apart.
	funcs := failOnlyCRUpdate(1, &writes)
	r, c, v, _ := midFailoverCluster(t, "hfr-statefail", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  rfc3339Ago(failoverResetMinWait + time.Second),
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	require.Error(t, result.Error)
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"))
	assert.Equal(t, stateFailoverReset, crGet(t, c, "hfr-statefail").Annotations[annotationRollingUpdateState])
}

func TestHandleFailoverRetrigger_SurfacesTheTimestampWriteFailure(t *testing.T) {
	before := rfc3339Ago(failoverResetMinWait + time.Minute)
	writes := 0
	funcs := failCRUpdateFrom(2, &writes)
	r, c, v, _ := midFailoverCluster(t, "hfr-tsfail", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  before,
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	require.Error(t, result.Error)
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"))
	assert.Equal(t, before, crGet(t, c, "hfr-tsfail").Annotations[annotationFailoverTimestamp],
		"the old deadline stands when the new one could not be written")
}

func TestHandleFailoverRetrigger_RejectedFailoverCommandIsNotFatal(t *testing.T) {
	r, c, v, _ := midFailoverCluster(t, "hfr-reject", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  rfc3339Ago(failoverResetMinWait + time.Second),
	}, nil)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) >= 2 && strings.EqualFold(args[0], "SENTINEL") && strings.EqualFold(args[1], "FAILOVER") {
			return respErr("INPROG Failover already in progress")
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	require.NoError(t, result.Error)
	assert.Equal(t, 15*time.Second, result.RequeueAfter)
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hfr-reject").Annotations[annotationRollingUpdateState])
}

// --- verifyNewMasterReady ---------------------------------------------------

// verifiedMasterCluster is the post-failover shape: pod-0 is the old master still
// waiting to be replaced, pod-1 has been promoted.
func verifiedMasterCluster(t *testing.T, name string, promotedInfo *valkeyclient.ReplicationInfo,
	funcs *interceptor.Funcs) (*ValkeyReconciler, client.Client, *vkov1.Valkey, []podState, *respRouter) {
	t.Helper()

	r, c, v, pods := midFailoverCluster(t, name, nil, funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		name + "-1": promotedInfo,
		name + "-2": replicaInfo(),
	}}
	return r, c, v, pods, router
}

// Nothing reports master yet: the old master must not be deleted on the assumption
// that the failover will finish.
func TestVerifyNewMasterReady_RejectsWhenNoUpdatedMasterExists(t *testing.T) {
	r, _, v, pods, router := verifiedMasterCluster(t, "vnm-none", replicaInfo(), nil)

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.False(t, verified)
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.targetsFor("DBSIZE"), "nothing to check when no promoted master exists")
}

// The pod that is about to be deleted still reports master until sentinel promotes
// someone else. Accepting it would let the update verify the old master against
// itself and delete the only copy of the data.
func TestVerifyNewMasterReady_IgnoresTheOldMasterAwaitingReplacement(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "vnm-old", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"vnm-old-0": masterInfo(2), // still the master, still on the old image
		"vnm-old-1": replicaInfo(),
		"vnm-old-2": replicaInfo(),
	}}

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.False(t, verified, "a pod that still needs the update cannot be the new master")
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.targetsFor("DBSIZE"))
}

// A promoted pod that is not ready yet is not a master to hand the cluster to.
func TestVerifyNewMasterReady_IgnoresPodsThatAreNotReady(t *testing.T) {
	r, _, v, pods, router := verifiedMasterCluster(t, "vnm-notready", masterInfo(2), nil)
	pods[1].ready = false

	verified, _ := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.False(t, verified)
	assert.Empty(t, router.targetsFor("DBSIZE"))
}

// An unreadable replication state is skipped rather than assumed to be a replica.
func TestVerifyNewMasterReady_SkipsPodsWithoutReplicationInfo(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "vnm-noinfo", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		// pod-1 is unreachable; pod-2 answers and is the promoted master.
		"vnm-noinfo-2": masterInfo(1),
	}}

	verified, _ := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.True(t, verified, "an unreachable pod must not hide the master behind it")
	assert.Equal(t, []string{dataAddr(v, 2)}, router.targetsFor("DBSIZE"))
}

// A master with no attached replicas means the replicas have not switched over yet.
// Deleting the old master now leaves the data unreplicated.
func TestVerifyNewMasterReady_RejectsAMasterWithoutConnectedReplicas(t *testing.T) {
	r, _, v, pods, router := verifiedMasterCluster(t, "vnm-lonely", masterInfo(0), nil)

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.False(t, verified)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.targetsFor("DBSIZE"), "no point checking the keyspace of an unreplicated master")
}

func TestVerifyNewMasterReady_RejectsAMasterStillSyncing(t *testing.T) {
	syncing := masterInfo(2)
	syncing.MasterSyncInProgress = true
	r, _, v, pods, router := verifiedMasterCluster(t, "vnm-syncing", syncing, nil)

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.False(t, verified)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.targetsFor("DBSIZE"))
}

// DBSIZE is the check that catches a promotion of an empty replica. If it cannot be
// read, the master counts as unverified.
func TestVerifyNewMasterReady_RejectsWhenTheKeyspaceCannotBeRead(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "vnm-dbsize", nil, nil)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) > 0 && strings.EqualFold(args[0], "DBSIZE") {
			return respErr("LOADING Valkey is loading the dataset in memory")
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"vnm-dbsize-1": masterInfo(2),
		"vnm-dbsize-2": replicaInfo(),
	}}

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.False(t, verified)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Equal(t, []string{dataAddr(v, 1)}, router.targetsFor("DBSIZE"))
}

// A missing TLS secret makes every client unusable, which is a reason to wait, not
// a reason to skip the verification.
func TestVerifyNewMasterReady_RejectsWhenTheTLSConfigCannotBeBuilt(t *testing.T) {
	v := sentinelClusterCR("vnm-tls", 3, func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "cluster-ca"},
			},
		}
	})
	pod1 := createPodForSts(v, 1, newValkeyImage, true)
	r, _ := newTestReconciler(v, pod1) // no TLS secret exists
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"vnm-tls-1": masterInfo(2),
	}}

	pods := []podState{{name: pod1.Name, pod: pod1, ready: true, exists: true}}
	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.False(t, verified)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent(), "no command is sent when the client cannot be built")
}

func TestVerifyNewMasterReady_AcceptsAMasterWithReplicasAndData(t *testing.T) {
	r, _, v, pods, router := verifiedMasterCluster(t, "vnm-ok", masterInfo(2), nil)

	verified, result := r.verifyNewMasterReady(context.Background(), v, pods, r.getInstanceChecker())

	assert.True(t, verified)
	assert.Equal(t, RollingUpdateResult{}, result, "a verified master produces no requeue of its own")
	assert.Equal(t, []string{dataAddr(v, 1)}, router.targetsFor("DBSIZE"),
		"the keyspace of the promoted pod is what has to be non-empty")
}

// --- replaceRemainingPods ---------------------------------------------------

func TestReplaceRemainingPods_WaitsForAPodThatIsGone(t *testing.T) {
	r, c, v, pods, _ := verifiedMasterCluster(t, "rrp-missing", masterInfo(2), nil)
	pods[0].exists = false

	result := r.replaceRemainingPods(context.Background(), v, pods)

	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, crGet(t, c, "rrp-missing").Annotations[annotationRollingUpdateState])
}

func TestReplaceRemainingPods_WaitsForAPodThatIsNotReady(t *testing.T) {
	r, c, v, pods, _ := verifiedMasterCluster(t, "rrp-notready", masterInfo(2), nil)
	pods[0].ready = false

	result := r.replaceRemainingPods(context.Background(), v, pods)

	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.True(t, podExists(t, c, "rrp-notready-0"))
	assert.Empty(t, crGet(t, c, "rrp-notready").Annotations[annotationRollingUpdateState])
}

// The guard that keeps the rolling update from losing the dataset: as long as the
// promoted master has no replicas attached, the old master stays.
func TestReplaceRemainingPods_KeepsTheOldMasterUntilTheNewOneIsVerified(t *testing.T) {
	r, c, v, pods, _ := verifiedMasterCluster(t, "rrp-guard", masterInfo(0), nil)

	result := r.replaceRemainingPods(context.Background(), v, pods)

	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.True(t, podExists(t, c, "rrp-guard-0"),
		"deleting the old master before the new one has replicas loses the only copy")
	assert.Empty(t, crGet(t, c, "rrp-guard").Annotations[annotationRollingUpdateState])
}

func TestReplaceRemainingPods_DeletesTheOldMasterOnceTheNewOneIsVerified(t *testing.T) {
	r, c, v, pods, router := verifiedMasterCluster(t, "rrp-go", masterInfo(2), nil)

	result := r.replaceRemainingPods(context.Background(), v, pods)

	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.False(t, podExists(t, c, "rrp-go-0"), "the former master is the last pod to be replaced")
	assert.True(t, podExists(t, c, "rrp-go-1"))
	assert.Equal(t, stateReplacingMaster, crGet(t, c, "rrp-go").Annotations[annotationRollingUpdateState])
	assert.Equal(t, []string{dataAddr(v, 1)}, router.targetsFor("DBSIZE"))
}

// Without sentinel there is no promoted master to verify against, so the
// verification is skipped entirely.
func TestReplaceRemainingPods_WithoutSentinelSkipsTheMasterVerification(t *testing.T) {
	v := newTestValkey("rrp-plain", testNamespace, func(v *vkov1.Valkey) {
		v.Spec.Replicas = 2
		v.Spec.Image = newValkeyImage
	})
	pod0 := createPodForSts(v, 0, oldValkeyImage, true)
	pod1 := createPodForSts(v, 1, newValkeyImage, true)
	r, c := newTestReconciler(v, pod0, pod1)

	pods := []podState{
		{name: pod0.Name, pod: pod0, needsUpdate: true, ready: true, exists: true},
		{name: pod1.Name, pod: pod1, ready: true, exists: true},
	}

	result := r.replaceRemainingPods(context.Background(), crGet(t, c, "rrp-plain"), pods)

	require.NoError(t, result.Error)
	assert.False(t, podExists(t, c, "rrp-plain-0"))
	assert.Equal(t, stateReplacingMaster, crGet(t, c, "rrp-plain").Annotations[annotationRollingUpdateState])
}

// The state is written before the delete so a crash between the two is recoverable.
// If the write fails, nothing is deleted.
func TestReplaceRemainingPods_SurfacesTheStateWriteFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	r, c, v, pods, _ := verifiedMasterCluster(t, "rrp-statefail", masterInfo(2), &funcs)

	result := r.replaceRemainingPods(context.Background(), v, pods)

	require.Error(t, result.Error)
	assert.True(t, podExists(t, c, "rrp-statefail-0"),
		"an unrecorded master replacement must not start")
}

func TestReplaceRemainingPods_SurfacesTheDeleteFailure(t *testing.T) {
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			return apierrors.NewInternalError(fmt.Errorf("pod delete rejected for %s", obj.GetName()))
		},
	}
	r, _, v, pods, _ := verifiedMasterCluster(t, "rrp-delfail", masterInfo(2), &funcs)

	result := r.replaceRemainingPods(context.Background(), v, pods)

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "deleting pod rrp-delfail-0")
}

func TestReplaceRemainingPods_RequeuesWhenNothingIsLeftToReplace(t *testing.T) {
	r, c, v, pods, _ := verifiedMasterCluster(t, "rrp-done", masterInfo(2), nil)
	pods[0].needsUpdate = false

	result := r.replaceRemainingPods(context.Background(), v, pods)

	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.True(t, podExists(t, c, "rrp-done-0"))
	assert.Empty(t, crGet(t, c, "rrp-done").Annotations[annotationRollingUpdateState])
}

// --- handleNewMasterFound ---------------------------------------------------

func TestHandleNewMasterFound_WithoutReplicasWaitsInsteadOfReplacing(t *testing.T) {
	r, c, v, pods, _ := verifiedMasterCluster(t, "hnmf-lonely", masterInfo(0), nil)

	result := r.handleNewMasterFound(context.Background(), v, pods[1], masterInfo(0), pods)

	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.True(t, podExists(t, c, "hnmf-lonely-0"))
}

func TestHandleNewMasterFound_WithReplicasReplacesTheOldMaster(t *testing.T) {
	r, c, v, pods, _ := verifiedMasterCluster(t, "hnmf-go", masterInfo(2), nil)

	result := r.handleNewMasterFound(context.Background(), v, pods[1], masterInfo(2), pods)

	require.NoError(t, result.Error)
	assert.False(t, podExists(t, c, "hnmf-go-0"))
	assert.Equal(t, stateReplacingMaster, crGet(t, c, "hnmf-go").Annotations[annotationRollingUpdateState])
}

// --- handleMasterWithNoReplicas ---------------------------------------------

// Sentinel sometimes never reconfigures the replicas onto the promoted master. Once
// the reconnect budget is spent the operator stops waiting for it: it sends REPLICAOF
// to every other pod itself and rebuilds sentinel's view around the real master.
func TestHandleMasterWithNoReplicas_ForcesReconnectAndResetsSentinelOnTimeout(t *testing.T) {
	before := rfc3339Ago(replicaReconnectTimeout + time.Minute)
	r, c, v, pods := midFailoverCluster(t, "hmnr-reset", map[string]string{
		annotationRollingUpdateState:       stateFailoverTriggered,
		annotationFailoverTimestamp:        before,
		annotationSentinelAwarenessStarted: rfc3339Ago(time.Minute),
	}, nil)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)

	result := r.handleMasterWithNoReplicas(context.Background(), v, pods[1], pods)

	require.NoError(t, result.Error)
	assert.Equal(t, 15*time.Second, result.RequeueAfter)

	promoted := podFQDN(v, 1)
	assert.ElementsMatch(t, []string{dataAddr(v, 0), dataAddr(v, 2)}, router.targetsFor("REPLICAOF"),
		"every pod except the promoted master is told to replicate from it")
	assert.Contains(t, router.commandsTo(dataAddr(v, 0)),
		fmt.Sprintf("REPLICAOF %s %d", promoted, builder.ServicePort(v)))
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR %s %s %d 2", builder.SentinelMonitorName(v), promoted, builder.ValkeyPort),
		"sentinel is re-pointed at the promoted master, not at pod-0")

	stored := crGet(t, c, "hmnr-reset")
	assert.Equal(t, "1", stored.Annotations[annotationReconnectResetCount])
	assert.True(t,
		mustParseRFC3339(t, stored.Annotations[annotationFailoverTimestamp]).After(mustParseRFC3339(t, before)),
		"the next reconnect window starts at this reset")
	assert.NotContains(t, stored.Annotations, annotationSentinelAwarenessStarted,
		"awareness has to be re-measured after a SENTINEL REMOVE+MONITOR")
}

// After maxReconnectResets the operator stops resetting and hands over to the
// rolling update -- but verifyNewMasterReady still holds the deletion back, which is
// what makes proceeding safe.
func TestHandleMasterWithNoReplicas_ProceedsAfterTheLastResetButStillGatesTheDelete(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hmnr-max", map[string]string{
		annotationFailoverTimestamp:   rfc3339Ago(replicaReconnectTimeout + time.Minute),
		annotationReconnectResetCount: strconv.Itoa(maxReconnectResets),
	}, nil)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hmnr-max-1": masterInfo(0), // still no replicas attached
		"hmnr-max-2": replicaInfo(),
	}}

	result := r.handleMasterWithNoReplicas(context.Background(), v, pods[1], pods)

	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter,
		"the pass fell through to replaceRemainingPods, whose verification requeues")
	assert.NotEmpty(t, router.targetsFor("REPLICAOF"), "the last attempt still forces the reconnect")
	assert.True(t, podExists(t, c, "hmnr-max-0"),
		"proceeding is only safe because the master verification still blocks the delete")

	stored := crGet(t, c, "hmnr-max")
	assert.NotContains(t, stored.Annotations, annotationReconnectResetCount,
		"the counter is spent and must not leak into the next rolling update")
}

func TestHandleMasterWithNoReplicas_SurfacesTheResetCountWriteFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	r, _, v, pods := midFailoverCluster(t, "hmnr-countfail", map[string]string{
		annotationFailoverTimestamp: rfc3339Ago(replicaReconnectTimeout + time.Minute),
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)

	result := r.handleMasterWithNoReplicas(context.Background(), v, pods[1], pods)

	require.Error(t, result.Error)
	assert.False(t, result.NeedsRequeue)
}

func TestHandleMasterWithNoReplicas_SurfacesTheCounterClearFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	r, c, v, pods := midFailoverCluster(t, "hmnr-clearfail", map[string]string{
		annotationFailoverTimestamp:   rfc3339Ago(replicaReconnectTimeout + time.Minute),
		annotationReconnectResetCount: strconv.Itoa(maxReconnectResets),
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(0))
	router.attach(r)

	result := r.handleMasterWithNoReplicas(context.Background(), v, pods[1], pods)

	require.Error(t, result.Error)
	assert.True(t, podExists(t, c, "hmnr-clearfail-0"), "no pod is replaced on a failed pass")
}

// --- handleNoMasterFound ----------------------------------------------------

func TestHandleNoMasterFound_WaitsWhileTheFailoverIsStillYoung(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hnmf-young", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(time.Second),
	}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleNoMasterFound(context.Background(), v, pods)

	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent(), "sentinel is left alone while the failover can still land")
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hnmf-young").Annotations[annotationRollingUpdateState])
}

// A failover that never produced a master is retried, and the retry starts by
// rebuilding sentinel's view around the master the operator can actually see.
func TestHandleNoMasterFound_ResetsSentinelAtTheKnownMasterAndSchedulesARetry(t *testing.T) {
	before := rfc3339Ago(failoverRetryTimeout + time.Minute)
	r, c, v, pods := midFailoverCluster(t, "hnmf-retry", map[string]string{
		annotationRollingUpdateState:       stateFailoverTriggered,
		annotationFailoverTimestamp:        before,
		annotationSentinelAwarenessStarted: rfc3339Ago(time.Minute),
	}, nil)
	pods[0].isMaster = false
	pods[2].isMaster = true // the failover did land somewhere, just not on a new-image pod
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleNoMasterFound(context.Background(), v, pods)

	require.NoError(t, result.Error)
	assert.Equal(t, 15*time.Second, result.RequeueAfter)
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR %s %s %d 2", builder.SentinelMonitorName(v), podFQDN(v, 2), builder.ValkeyPort),
		"re-monitoring the wrong pod would make sentinel demote the real master")

	stored := crGet(t, c, "hnmf-retry")
	assert.Equal(t, stateFailoverReset, stored.Annotations[annotationRollingUpdateState])
	assert.True(t,
		mustParseRFC3339(t, stored.Annotations[annotationFailoverTimestamp]).After(mustParseRFC3339(t, before)),
		"the retrigger wait is measured from this reset")
	assert.NotContains(t, stored.Annotations, annotationSentinelAwarenessStarted)
}

// With no master visible at all, sentinel is re-pointed at the default pod-0 address
// rather than at nothing.
func TestHandleNoMasterFound_FallsBackToTheDefaultMasterAddress(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "hnmf-fallback", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(failoverRetryTimeout + time.Minute),
	}, nil)
	for i := range pods {
		pods[i].isMaster = false
	}
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleNoMasterFound(context.Background(), v, pods)

	require.NoError(t, result.Error)
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR %s %s %d 2",
			builder.SentinelMonitorName(v), builder.MasterAddress(v), builder.ValkeyPort))
}

func TestHandleNoMasterFound_SurfacesTheTimestampWriteFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	r, c, v, pods := midFailoverCluster(t, "hnmf-tsfail", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(failoverRetryTimeout + time.Minute),
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleNoMasterFound(context.Background(), v, pods)

	require.Error(t, result.Error)
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hnmf-tsfail").Annotations[annotationRollingUpdateState],
		"the retry state must not be entered without a deadline to measure it against")
}

func TestHandleNoMasterFound_SurfacesTheStateWriteFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(2, &writes)
	r, c, v, pods := midFailoverCluster(t, "hnmf-statefail", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(failoverRetryTimeout + time.Minute),
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleNoMasterFound(context.Background(), v, pods)

	require.Error(t, result.Error)
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hnmf-statefail").Annotations[annotationRollingUpdateState])
}

// --- resetSentinelState -----------------------------------------------------

func TestResetSentinelState_ReconfiguresEverySentinelAroundTheGivenMaster(t *testing.T) {
	v := sentinelClusterCR("rss-all", 3)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	master := podFQDN(v, 1)
	r.resetSentinelState(context.Background(), v, master)

	want := []string{
		"SENTINEL REMOVE rss-all",
		fmt.Sprintf("SENTINEL MONITOR rss-all %s %d 2", master, builder.ValkeyPort),
		fmt.Sprintf("SENTINEL SET rss-all down-after-milliseconds %d", builder.SentinelDownAfterMilliseconds),
		fmt.Sprintf("SENTINEL SET rss-all failover-timeout %d", builder.SentinelFailoverTimeout),
		fmt.Sprintf("SENTINEL SET rss-all parallel-syncs %d", builder.SentinelParallelSyncs),
		"SENTINEL SET rss-all resolve-hostnames yes",
		"SENTINEL SET rss-all announce-hostnames yes",
	}
	for ordinal := 0; ordinal < 3; ordinal++ {
		assert.Equal(t, want, router.commandsTo(sentinelAddr(v, ordinal, builder.SentinelPort)),
			"sentinel-%d must be reconfigured like its peers", ordinal)
	}
}

// REMOVE+MONITOR preserves the current master; a plain RESET would revert sentinel to
// the pod-0 address from its config file. RESET is only the fallback when the
// removal itself failed.
func TestResetSentinelState_FallsBackToResetWhenRemoveFails(t *testing.T) {
	v := sentinelClusterCR("rss-fallback", 1)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) >= 2 && strings.EqualFold(args[1], "REMOVE") {
			return respErr("No such master with that name")
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	r.resetSentinelState(context.Background(), v, podFQDN(v, 0))

	assert.Equal(t, []string{"SENTINEL REMOVE rss-fallback", "SENTINEL RESET rss-fallback"},
		router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		"a failed removal must not be followed by a monitor add on the same sentinel")
}

// A monitor that was not added cannot be configured, so the SET sequence is skipped.
func TestResetSentinelState_SkipsTheParametersWhenTheMonitorAddFails(t *testing.T) {
	v := sentinelClusterCR("rss-nomonitor", 1)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) >= 2 && strings.EqualFold(args[1], "MONITOR") {
			return respErr("Duplicate master name")
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	r.resetSentinelState(context.Background(), v, podFQDN(v, 0))

	assert.Empty(t, router.targetsFor("SENTINEL SET"))
}

// Without auth-pass sentinel cannot authenticate against the monitored master, marks
// it disconnected and never discovers the replicas -- which is the NOGOODSLAVE the
// whole reset exists to clear.
func TestResetSentinelState_RestoresTheAuthPassword(t *testing.T) {
	v := sentinelClusterCR("rss-auth", 1, func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "valkey-auth", SecretPasswordKey: "password"}
	})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "valkey-auth", Namespace: testNamespace},
		Data:       map[string][]byte{"password": []byte("s3cret")},
	}
	r, _ := newTestReconciler(v, secret)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	r.resetSentinelState(context.Background(), v, podFQDN(v, 0))

	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		"SENTINEL SET rss-auth auth-pass s3cret")
}

func TestResetSentinelState_OmitsAuthPassWhenNoPasswordIsConfigured(t *testing.T) {
	v := sentinelClusterCR("rss-noauth", 1)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	r.resetSentinelState(context.Background(), v, podFQDN(v, 0))

	for _, cmd := range router.sent() {
		assert.NotContains(t, cmd, "auth-pass")
	}
}

func TestResetSentinelState_FallsBackToTheDefaultMasterAddress(t *testing.T) {
	v := sentinelClusterCR("rss-default", 1)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	r.resetSentinelState(context.Background(), v, "")

	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR rss-default %s %d 1", builder.MasterAddress(v), builder.ValkeyPort),
		"a single sentinel yields a quorum of one")
}

// The quorum written back has to match the one the sentinel ConfigMap would compute,
// or the reset silently changes the failover threshold of the cluster.
func TestResetSentinelState_DerivesTheQuorumFromTheSentinelReplicaCount(t *testing.T) {
	v := sentinelClusterCR("rss-quorum", 5)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, healthyCluster(4))
	router.attach(r)

	r.resetSentinelState(context.Background(), v, podFQDN(v, 0))

	assert.Len(t, router.targetsFor("SENTINEL REMOVE"), 5, "every sentinel replica is reconfigured")
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 4, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR rss-quorum %s %d 3", podFQDN(v, 0), builder.ValkeyPort),
		"five sentinels need a quorum of three")
}

// With TLS on, both the sentinel port dialed and the master port handed to
// SENTINEL MONITOR have to be the encrypted ones.
func TestResetSentinelState_UsesTheTLSPortsWhenTLSIsEnabled(t *testing.T) {
	v := sentinelClusterCR("rss-tls", 1, func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "cluster-ca"},
			},
		}
	})
	secret := newTestSentinelTLSSecret(builder.SentinelTLSSecretName(v), testNamespace)
	r, _ := newTestReconciler(v, secret)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	r.resetSentinelState(context.Background(), v, podFQDN(v, 0))

	assert.Equal(t, []string{sentinelAddr(v, 0, builder.SentinelTLSPort)}, router.targetsFor("SENTINEL REMOVE"))
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelTLSPort)),
		fmt.Sprintf("SENTINEL MONITOR rss-tls %s %d 1", podFQDN(v, 0), builder.TLSPort))
}

// A sentinel whose TLS config cannot be built is skipped, and its peers are still
// reconfigured.
func TestResetSentinelState_SkipsSentinelsWhoseTLSConfigIsUnavailable(t *testing.T) {
	v := sentinelClusterCR("rss-notls", 3, func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "cluster-ca"},
			},
		}
	})
	r, _ := newTestReconciler(v) // the sentinel TLS secret does not exist
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	r.resetSentinelState(context.Background(), v, podFQDN(v, 0))

	assert.Empty(t, router.sent(), "no sentinel can be reached without a usable client")
}

// --- handlePostFailover -----------------------------------------------------

func TestHandlePostFailover_SurfacesAMissingStatefulSet(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "hpf-nosts", nil, nil)

	result := r.handlePostFailover(context.Background(), v, pods, 0)

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "getting StatefulSet in post-failover")
}

// The whole post-failover leg in one pass: fresh pod states are re-collected (roles
// changed), the promoted pod is recognised, verified, and the old master deleted.
func TestHandlePostFailover_VerifiesThePromotedMasterAndReplacesTheOldOne(t *testing.T) {
	v := sentinelClusterCR("hpf-go", 3)
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod0.Spec.Containers[0].Image = oldValkeyImage // the former master, not replaced yet
	pod1 := podFromStsTemplate(v, sts, 1)
	pod2 := podFromStsTemplate(v, sts, 2)
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(time.Second),
	}

	r, c := newTestReconciler(v, sts, pod0, pod1, pod2)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hpf-go-0": replicaInfo(),
		"hpf-go-1": masterInfo(2),
		"hpf-go-2": replicaInfo(),
	}}

	result := r.handlePostFailover(context.Background(), crGet(t, c, "hpf-go"), nil, -1)

	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.False(t, podExists(t, c, "hpf-go-0"), "the former master is replaced once the new one is verified")
	assert.Equal(t, []string{dataAddr(v, 1)}, router.targetsFor("DBSIZE"))
	assert.Equal(t, stateReplacingMaster, crGet(t, c, "hpf-go").Annotations[annotationRollingUpdateState])
}

// --- hasPendingUpdates / deleteNextPendingPod -------------------------------

func TestHasPendingUpdates(t *testing.T) {
	assert.False(t, hasPendingUpdates(nil))
	assert.False(t, hasPendingUpdates([]podState{{name: "a"}, {name: "b"}}))
	assert.True(t, hasPendingUpdates([]podState{{name: "a"}, {name: "b", needsUpdate: true}}))
}

func TestDeleteNextPendingPod_DeletesTheFirstReadyPodThatNeedsUpdating(t *testing.T) {
	v := newTestValkey("dnp", testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	pod1 := createPodForSts(v, 1, oldValkeyImage, true)
	pod2 := createPodForSts(v, 2, oldValkeyImage, true)
	r, c := newTestReconciler(v, pod1, pod2)

	pods := []podState{
		{name: "dnp-0", needsUpdate: true}, // gone, skipped
		{name: pod1.Name, pod: pod1, needsUpdate: true, ready: true, exists: true},
		{name: pod2.Name, pod: pod2, needsUpdate: true, ready: true, exists: true},
	}

	result := r.deleteNextPendingPod(context.Background(), pods)

	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.False(t, podExists(t, c, "dnp-1"))
	assert.True(t, podExists(t, c, "dnp-2"), "only one pod is replaced per pass")
}

func TestDeleteNextPendingPod_RequeuesWhenNoCandidateIsReady(t *testing.T) {
	v := newTestValkey("dnp-none", testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 2 })
	pod1 := createPodForSts(v, 1, oldValkeyImage, false)
	r, c := newTestReconciler(v, pod1)

	pods := []podState{
		{name: "dnp-none-0", needsUpdate: true},                       // does not exist
		{name: pod1.Name, pod: pod1, needsUpdate: true, exists: true}, // not ready
	}

	result := r.deleteNextPendingPod(context.Background(), pods)

	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.True(t, podExists(t, c, "dnp-none-1"), "a pod that is not ready must not be deleted on top")
}

func TestDeleteNextPendingPod_SurfacesTheDeleteFailure(t *testing.T) {
	v := newTestValkey("dnp-fail", testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	pod0 := createPodForSts(v, 0, oldValkeyImage, true)
	r, _ := newInterceptedReconciler(interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			return apierrors.NewInternalError(fmt.Errorf("delete of %s rejected", obj.GetName()))
		},
	}, v, pod0)

	result := r.deleteNextPendingPod(context.Background(),
		[]podState{{name: pod0.Name, pod: pod0, needsUpdate: true, ready: true, exists: true}})

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "deleting pod dnp-fail-0")
}

// --- getSentinelMasterPodName -----------------------------------------------

// respSentinelMasterAt is SENTINEL MASTER including the announced address, which is
// what the operator reads the master pod name out of.
func respSentinelMasterAt(ip string, numSlaves int) string {
	return respBulkArray(
		"name", "mymaster",
		"ip", ip,
		"flags", "master",
		"num-slaves", strconv.Itoa(numSlaves),
		"quorum", "2",
	)
}

// With announce-hostnames on -- which the operator configures on every reset --
// sentinel answers with the pod FQDN and the first label is the pod name.
func TestGetSentinelMasterPodName_ReturnsThePodSentinelAnnounces(t *testing.T) {
	v := sentinelClusterCR("gsm", 3)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) >= 2 && strings.EqualFold(args[1], "MASTER") {
			return respSentinelMasterAt(podFQDN(v, 2), 2)
		}
		return respOK
	})
	router.attach(r)

	assert.Equal(t, "gsm-2", r.getSentinelMasterPodName(context.Background(), v))
	assert.Equal(t, []string{sentinelAddr(v, 0, builder.SentinelPort)}, router.targetsFor("SENTINEL MASTER"),
		"the first sentinel that answers is authoritative")
}

// A sentinel that does not answer is skipped, and the next one decides.
func TestGetSentinelMasterPodName_SkipsSentinelsThatDoNotAnswer(t *testing.T) {
	v := sentinelClusterCR("gsm-skip", 3)
	r, _ := newTestReconciler(v)
	dead := sentinelAddr(v, 0, builder.SentinelPort)
	router := newRESPRouter(t, func(target string, args []string) string {
		if target == dead {
			return ""
		}
		if len(args) >= 2 && strings.EqualFold(args[1], "MASTER") {
			return respSentinelMasterAt(podFQDN(v, 1), 2)
		}
		return respOK
	})
	router.attach(r)

	assert.Equal(t, "gsm-skip-1", r.getSentinelMasterPodName(context.Background(), v))
}

// Without announce-hostnames sentinel answers with an IP, and splitting it at the
// first dot yields "10" -- a name no pod ever has. The split-brain resolver then
// finds no authority and falls back to counting connected slaves.
//
// The operator sets announce-hostnames on every sentinel it configures, so this is
// the behaviour on a cluster whose sentinels it has not (yet) touched.
func TestGetSentinelMasterPodName_AnIPAddressIsTruncatedToItsFirstOctet(t *testing.T) {
	v := sentinelClusterCR("gsm-ip", 1)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) >= 2 && strings.EqualFold(args[1], "MASTER") {
			return respSentinelMasterAt("10.42.0.7", 2)
		}
		return respOK
	})
	router.attach(r)

	assert.Equal(t, "10", r.getSentinelMasterPodName(context.Background(), v),
		"an IP answer cannot name a pod, so the resolver falls back to slave counts")
}

// --- checkFinalizationTopology ----------------------------------------------

// finalizingCluster is the end of a Sentinel rolling update: every pod runs the new
// image, pod-1 is the master the failover left behind.
func finalizingCluster(t *testing.T, name string, annotations map[string]string,
	funcs *interceptor.Funcs) (*ValkeyReconciler, client.Client, *vkov1.Valkey, []podState) {
	t.Helper()

	v := sentinelClusterCR(name, 3)
	v.Annotations = annotations

	pod0 := createPodForSts(v, 0, newValkeyImage, true)
	pod1 := createPodForSts(v, 1, newValkeyImage, true)
	pod2 := createPodForSts(v, 2, newValkeyImage, true)
	objs := []client.Object{v, pod0, pod1, pod2}

	var r *ValkeyReconciler
	var c client.Client
	if funcs != nil {
		r, c = newInterceptedReconciler(*funcs, objs...)
	} else {
		r, c = newTestReconciler(objs...)
	}

	pods := []podState{
		{name: pod0.Name, pod: pod0, ready: true, exists: true},
		{name: pod1.Name, pod: pod1, ready: true, exists: true, isMaster: true},
		{name: pod2.Name, pod: pod2, ready: true, exists: true},
	}
	return r, c, crGet(t, c, name), pods
}

// Two masters, or none, means the cluster has not settled. Resetting sentinel on
// that picture would pin it to the wrong pod, so the pass waits and arms the bound
// that caps the waiting.
func TestCheckFinalizationTopology_WaitsForASingleMaster(t *testing.T) {
	r, c, v, pods := finalizingCluster(t, "cft-wait", nil, nil)
	pods[2].isMaster = true // split brain still visible
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.checkFinalizationTopology(context.Background(), v, pods)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent(), "sentinel must not be repointed at an unsettled topology")
	assert.NotEmpty(t, crGet(t, c, "cft-wait").Annotations[annotationFinalizationTimestamp],
		"the finalization bound has to be armed or the wait can never be declared stalled")
}

// Once that wait is stalled the update stops blocking on a topology it cannot read
// and resets sentinel best-effort, which falls back to the pod-0 address.
func TestCheckFinalizationTopology_ProceedsWithABestEffortResetWhenStalled(t *testing.T) {
	r, _, v, pods := finalizingCluster(t, "cft-stalled", map[string]string{
		annotationFinalizationTimestamp: rfc3339Ago(finalizationStallTimeout + time.Minute),
	}, nil)
	pods[1].isMaster = false // no master visible at all
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.checkFinalizationTopology(context.Background(), v, pods)

	assert.Nil(t, result, "a stalled finalization proceeds instead of requeueing forever")
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR cft-stalled %s %d 2", builder.MasterAddress(v), builder.ValkeyPort))
}

// --- syncSentinelWithMaster -------------------------------------------------

func TestSyncSentinelWithMaster_WaitsWhenReplicationCannotBeRead(t *testing.T) {
	r, c, v, pods := finalizingCluster(t, "ssm-noinfo", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	checker := &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{}}

	result := r.syncSentinelWithMaster(context.Background(), v, pods[1], 2, checker, false)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent())
	assert.NotEmpty(t, crGet(t, c, "ssm-noinfo").Annotations[annotationFinalizationTimestamp])
}

// Repointing sentinel while a replica is still detached would make it monitor a
// master it cannot see replicas for.
func TestSyncSentinelWithMaster_WaitsUntilEveryReplicaIsConnected(t *testing.T) {
	r, c, v, pods := finalizingCluster(t, "ssm-partial", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	checker := &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"ssm-partial-1": masterInfo(1),
	}}

	result := r.syncSentinelWithMaster(context.Background(), v, pods[1], 2, checker, false)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.targetsFor("SENTINEL MONITOR"))
	assert.NotEmpty(t, crGet(t, c, "ssm-partial").Annotations[annotationFinalizationTimestamp])
	assert.Empty(t, crGet(t, c, "ssm-partial").Annotations[builder.AnnotationKnownMaster],
		"an unconfirmed topology must not be published as the known master")
}

func TestSyncSentinelWithMaster_ProceedsWithAPartialSyncWhenStalled(t *testing.T) {
	r, _, v, pods := finalizingCluster(t, "ssm-stalled", map[string]string{
		annotationFinalizationTimestamp: rfc3339Ago(finalizationStallTimeout + time.Minute),
	}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	checker := &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"ssm-stalled-1": masterInfo(1),
	}}

	result := r.syncSentinelWithMaster(context.Background(), v, pods[1], 2, checker, true)

	assert.Nil(t, result)
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR ssm-stalled %s %d 2", podFQDN(v, 1), builder.ValkeyPort),
		"a partial sync still has to point sentinel at the real master")
}

// The two writes that make the failover survive a sentinel restart: the known-master
// annotation feeds the regenerated sentinel ConfigMap, the reset fixes the running
// sentinels. Both have to name the pod that actually holds the data.
func TestSyncSentinelWithMaster_PublishesTheConfirmedMasterAndRepointsSentinel(t *testing.T) {
	r, c, v, pods := finalizingCluster(t, "ssm-go", nil, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	checker := &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"ssm-go-1": masterInfo(2),
	}}

	result := r.syncSentinelWithMaster(context.Background(), v, pods[1], 2, checker, false)

	assert.Nil(t, result)
	assert.Equal(t, podFQDN(v, 1), crGet(t, c, "ssm-go").Annotations[builder.AnnotationKnownMaster],
		"a sentinel pod restarting after the update must come up monitoring pod-1, not pod-0")
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 2, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR ssm-go %s %d 2", podFQDN(v, 1), builder.ValkeyPort))
}

// The annotation is deliberately best-effort on the Sentinel path: sentinel itself is
// the master authority there, so a failed write must not stop the reset that
// actually repairs the topology.
func TestSyncSentinelWithMaster_ResetsSentinelEvenWhenTheAnnotationWriteFails(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	r, c, v, pods := finalizingCluster(t, "ssm-nowrite", nil, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	checker := &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"ssm-nowrite-1": masterInfo(2),
	}}

	result := r.syncSentinelWithMaster(context.Background(), v, pods[1], 2, checker, false)

	assert.Nil(t, result)
	assert.Empty(t, crGet(t, c, "ssm-nowrite").Annotations[builder.AnnotationKnownMaster])
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)),
		fmt.Sprintf("SENTINEL MONITOR ssm-nowrite %s %d 2", podFQDN(v, 1), builder.ValkeyPort))
	assert.Empty(t, v.Annotations[builder.AnnotationKnownMaster],
		"the in-memory copy must match what was persisted, not what was attempted")
}

// --- finalizeRollingUpdate --------------------------------------------------

// Completion clears every annotation the state machine wrote, or the next rolling
// update starts against a spent budget.
func TestFinalizeRollingUpdate_CompletesAndClearsTheState(t *testing.T) {
	r, c, v, pods := finalizingCluster(t, "fru-go", map[string]string{
		annotationRollingUpdateState:       stateReplacingMaster,
		annotationFailoverTimestamp:        rfc3339Ago(time.Minute),
		annotationReconnectResetCount:      "2",
		annotationSentinelAwarenessStarted: rfc3339Ago(time.Minute),
	}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"fru-go-1": masterInfo(2),
	}}

	result := r.finalizeRollingUpdate(context.Background(), v, pods)

	require.NoError(t, result.Error)
	assert.True(t, result.Completed)

	stored := crGet(t, c, "fru-go")
	for _, key := range []string{
		annotationRollingUpdateState,
		annotationFailoverTimestamp,
		annotationReconnectResetCount,
		annotationSentinelAwarenessStarted,
	} {
		assert.NotContains(t, stored.Annotations, key, "%s survived the completion", key)
	}
	assert.Equal(t, podFQDN(v, 1), stored.Annotations[builder.AnnotationKnownMaster],
		"the known master is the one annotation that outlives the update")
	assert.Contains(t, router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort)), "SENTINEL REMOVE fru-go")
}

// An unstable topology blocks the completion: reporting done here would clear the
// state machine while two pods still claim to be master.
func TestFinalizeRollingUpdate_DoesNotCompleteWhileTheTopologyIsUnstable(t *testing.T) {
	r, c, v, pods := finalizingCluster(t, "fru-unstable", map[string]string{
		annotationRollingUpdateState: stateReplacingMaster,
	}, nil)
	pods[0].isMaster = true
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.finalizeRollingUpdate(context.Background(), v, pods)

	assert.False(t, result.Completed)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Equal(t, stateReplacingMaster, crGet(t, c, "fru-unstable").Annotations[annotationRollingUpdateState])
}

// A rolling update that never failed over has nothing to reconcile with sentinel,
// so the topology check is skipped entirely.
func TestFinalizeRollingUpdate_SkipsTheTopologyCheckWithoutAFailover(t *testing.T) {
	r, _, v, pods := finalizingCluster(t, "fru-nofailover", nil, nil)
	pods[1].isMaster = false // nothing reports master, and it does not matter
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.finalizeRollingUpdate(context.Background(), v, pods)

	require.NoError(t, result.Error)
	assert.True(t, result.Completed)
	assert.Empty(t, router.sent(), "no failover happened, so sentinel needs no repair")
}

func TestFinalizeRollingUpdate_SurfacesTheStateClearFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	r, _, v, pods := finalizingCluster(t, "fru-clearfail", map[string]string{
		annotationRollingUpdateState: stateReplacingMaster,
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"fru-clearfail-1": masterInfo(2),
	}}

	result := r.finalizeRollingUpdate(context.Background(), v, pods)

	require.Error(t, result.Error)
	assert.False(t, result.Completed, "the update is not done while its state is still on the CR")
}

// --- replaceNextReplica -----------------------------------------------------

func TestReplaceNextReplica_ReturnsNilWhenOnlyTheMasterIsLeft(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "rnr-done", nil, nil)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"rnr-done-1": replicaInfo(),
		"rnr-done-2": replicaInfo(),
	}}

	assert.Nil(t, r.replaceNextReplica(context.Background(), v, pods),
		"a pending master is not a replica candidate")
	assert.True(t, podExists(t, c, "rnr-done-0"))
}

// A candidate that is not ready yet is waited for, not deleted: deleting it would
// take a second replica out at the same time.
func TestReplaceNextReplica_WaitsForACandidateThatIsNotReady(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "rnr-notready", nil, nil)
	pods[2].needsUpdate = true
	pods[2].ready = false
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"rnr-notready-1": replicaInfo(),
	}}

	result := r.replaceNextReplica(context.Background(), v, pods)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.True(t, podExists(t, c, "rnr-notready-2"))
	assert.Empty(t, crGet(t, c, "rnr-notready").Annotations[annotationRollingUpdateState])
}

// The state is claimed before the first replica is deleted, so a concurrent pass
// cannot start a second rolling update. A failed claim stops the pass.
func TestReplaceNextReplica_SurfacesTheStateWriteFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	r, c, v, pods := midFailoverCluster(t, "rnr-statefail", nil, &funcs)
	pods[2].needsUpdate = true
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"rnr-statefail-1": replicaInfo(),
	}}

	result := r.replaceNextReplica(context.Background(), v, pods)

	require.NotNil(t, result)
	require.Error(t, result.Error)
	assert.True(t, podExists(t, c, "rnr-statefail-2"), "no replica is deleted before the state is claimed")
}

func TestReplaceNextReplica_SurfacesTheDeleteFailure(t *testing.T) {
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			return apierrors.NewInternalError(fmt.Errorf("delete of %s rejected", obj.GetName()))
		},
	}
	r, _, v, pods := midFailoverCluster(t, "rnr-delfail", nil, &funcs)
	pods[2].needsUpdate = true
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"rnr-delfail-1": replicaInfo(),
	}}

	result := r.replaceNextReplica(context.Background(), v, pods)

	require.NotNil(t, result)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "deleting pod rnr-delfail-2")
}

// --- waitForWriteSync -------------------------------------------------------

// A cascaded chain (master -> replica -> replica) means not every replica
// acknowledges the master directly. waitForReplicasReady already confirmed all of
// them are synced, so the partial acknowledgement is accepted rather than retried
// until the rolling update times out.
func TestWaitForWriteSync_AcceptsAPartialAcknowledgement(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "wws-partial", nil, nil)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) > 0 && strings.EqualFold(args[0], "WAIT") {
			return respInt(1) // only one of the two replicas answered directly
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	assert.Nil(t, r.waitForWriteSync(context.Background(), v, pods, 0))
	assert.Equal(t, []string{fmt.Sprintf("WAIT 2 %d", waitWriteSyncTimeout)}, router.commandsTo(dataAddr(v, 0)))
}

// Without a usable TLS config there is no way to ask the master anything, and an
// unverifiable write state must not be read as "safe to fail over".
func TestWaitForWriteSync_RequeuesWhenTheTLSConfigCannotBeBuilt(t *testing.T) {
	v := sentinelClusterCR("wws-tls", 3, func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "cluster-ca"},
			},
		}
	})
	pod0 := createPodForSts(v, 0, oldValkeyImage, true)
	pod1 := createPodForSts(v, 1, newValkeyImage, true)
	r, _ := newTestReconciler(v, pod0, pod1) // no valkey TLS secret
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	pods := []podState{
		{name: pod0.Name, pod: pod0, needsUpdate: true, isMaster: true, ready: true, exists: true},
		{name: pod1.Name, pod: pod1, ready: true, exists: true},
	}
	result := r.waitForWriteSync(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent())
}

// --- handleRollingUpdate (the Sentinel orchestrator) ------------------------

// haRollingUpdate builds a Sentinel cluster whose persisted StatefulSet asks for the
// new image while pod-0 still runs the old one, and returns the reconciler together
// with the StatefulSet handleRollingUpdate reconciles against.
func haRollingUpdate(t *testing.T, name string, annotations map[string]string,
	outdated []int, funcs *interceptor.Funcs) (*ValkeyReconciler, client.Client, *vkov1.Valkey, *appsv1.StatefulSet) {
	t.Helper()

	v := sentinelClusterCR(name, 3)
	v.Annotations = annotations
	sts := stsForValkey(v)

	objs := []client.Object{v, sts}
	for i := 0; i < 3; i++ {
		pod := podFromStsTemplate(v, sts, i)
		for _, ordinal := range outdated {
			if ordinal == i {
				pod.Spec.Containers[0].Image = oldValkeyImage
			}
		}
		objs = append(objs, pod)
	}

	var r *ValkeyReconciler
	var c client.Client
	if funcs != nil {
		r, c = newInterceptedReconciler(*funcs, objs...)
	} else {
		r, c = newTestReconciler(objs...)
	}
	return r, c, crGet(t, c, name), sts
}

func TestHandleRollingUpdate_SurfacesAPodReadFailure(t *testing.T) {
	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, isPod := obj.(*corev1.Pod); isPod {
				return apierrors.NewServiceUnavailable("apiserver is down")
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}
	r, _, v, sts := haRollingUpdate(t, "hru-getfail", nil, []int{0}, &funcs)

	result := r.handleRollingUpdate(context.Background(), v, sts)

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "getting pod hru-getfail-0")
}

// State left behind by a previous rolling update is cleared before this one starts.
// If that clear cannot be written, the pass fails rather than running against a
// state machine position that does not describe the cluster.
func TestHandleRollingUpdate_SurfacesTheStaleStateClearFailure(t *testing.T) {
	writes := 0
	funcs := failCRUpdateFrom(1, &writes)
	// Nothing has been replaced yet, but the CR still claims a failover is in flight.
	r, _, v, sts := haRollingUpdate(t, "hru-stalefail", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
	}, []int{0, 1, 2}, &funcs)

	result := r.handleRollingUpdate(context.Background(), v, sts)

	require.Error(t, result.Error)
}

// The failover-reset state routes into the retrigger handler, which is the only
// place that waits out the sentinel rediscovery window.
func TestHandleRollingUpdate_RoutesFailoverResetToTheRetrigger(t *testing.T) {
	r, c, v, sts := haRollingUpdate(t, "hru-reset", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  rfc3339Ago(time.Second),
	}, []int{0}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleRollingUpdate(context.Background(), v, sts)

	assert.Equal(t, 10*time.Second, result.RequeueAfter,
		"the retrigger handler waits out failoverResetMinWait; no other branch requeues for 10s here")
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"))
	assert.Equal(t, stateFailoverReset, crGet(t, c, "hru-reset").Annotations[annotationRollingUpdateState])
}

// A triggered failover routes into the post-failover handler, which re-reads the
// roles and replaces the former master once the promoted one is verified.
func TestHandleRollingUpdate_RoutesTriggeredFailoverToPostFailover(t *testing.T) {
	r, c, v, sts := haRollingUpdate(t, "hru-post", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(time.Second),
	}, []int{0}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hru-post-0": replicaInfo(),
		"hru-post-1": masterInfo(2),
		"hru-post-2": replicaInfo(),
	}}

	result := r.handleRollingUpdate(context.Background(), v, sts)

	require.NoError(t, result.Error)
	assert.False(t, podExists(t, c, "hru-post-0"), "the former master is replaced last")
	assert.Equal(t, stateReplacingMaster, crGet(t, c, "hru-post").Annotations[annotationRollingUpdateState])
}

// With every replica already replaced, the orchestrator reaches the master failover
// and triggers it -- the step that makes the master replaceable at all.
func TestHandleRollingUpdate_TriggersTheMasterFailoverWhenOnlyTheMasterIsLeft(t *testing.T) {
	r, c, v, sts := haRollingUpdate(t, "hru-failover", map[string]string{
		annotationRollingUpdateState: stateReplacingReplicas,
	}, []int{0}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hru-failover-0": masterInfo(2),
		"hru-failover-1": replicaInfo(),
		"hru-failover-2": replicaInfo(),
	}}

	result := r.handleRollingUpdate(context.Background(), v, sts)

	require.NoError(t, result.Error)
	assert.Equal(t, 15*time.Second, result.RequeueAfter)
	assert.NotEmpty(t, router.targetsFor("SENTINEL FAILOVER"))
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "hru-failover").Annotations[annotationRollingUpdateState])
	assert.True(t, podExists(t, c, "hru-failover-0"),
		"the master is only deleted after the failover, never before it")
}

// Every pod already runs the new image but the master is not ready yet, so the
// update is neither complete nor has anything to delete: it waits.
func TestHandleRollingUpdate_WaitsWhenTheMasterIsUpdatedButNotReady(t *testing.T) {
	v := sentinelClusterCR("hru-wait", 3)
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod0.Status.Conditions = nil // updated, but not ready
	pod1 := podFromStsTemplate(v, sts, 1)
	pod2 := podFromStsTemplate(v, sts, 2)
	v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingReplicas}

	r, c := newTestReconciler(v, sts, pod0, pod1, pod2)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hru-wait-0": masterInfo(2),
		"hru-wait-1": replicaInfo(),
		"hru-wait-2": replicaInfo(),
	}}

	result := r.handleRollingUpdate(context.Background(), crGet(t, c, "hru-wait"), sts)

	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.True(t, podExists(t, c, "hru-wait-0"))
	assert.Empty(t, router.targetsFor("SENTINEL FAILOVER"),
		"an up-to-date master is not failed over just because it is not ready")
}
