package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The tests in this file pin ADR 0008 D4-D7: in a 2-replica cluster without Sentinel the
// manual failover promotes pod-1 and deletes pod-0, and the promoted pod has no
// replicas attached at that moment. The returning pod-0 therefore cannot identify
// pod-1 as master from peer state alone — the operator has to hand it the answer
// through the replica ConfigMap before the delete, or pod-0 boots as a second,
// independent master.

// fakeValkeyServer accepts RESP commands and answers every one of them, so that
// the failover path (REPLICAOF, WAIT) succeeds in a unit test.
func fakeValkeyServer(t *testing.T) string {
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
				n, readErr := conn.Read(buf)
				if readErr != nil {
					return
				}
				// WAIT and DBSIZE expect an integer reply; everything else is happy
				// with +OK. DBSIZE is what the pre-promotion key-count guard asks
				// (ADR 0007 D10), and a server that answers +OK to it would fail the
				// parse and stall every failover this fixture drives.
				request := strings.ToUpper(string(buf[:n]))
				switch {
				case strings.Contains(request, "WAIT"):
					_, _ = conn.Write([]byte(":1\r\n"))
				case strings.Contains(request, "DBSIZE"):
					_, _ = conn.Write([]byte(":4711\r\n"))
				default:
					_, _ = conn.Write([]byte("+OK\r\n"))
				}
			}()
		}
	}()

	return ln.Addr().String()
}

// twoReplicaFailoverFixture builds the ADR 0008 D4-D7 shape: pod-0 is the master and still
// needs the update, pod-1 is already updated and ready to be promoted.
func twoReplicaFailoverFixture(t *testing.T) (*vkov1.Valkey, []podState, *corev1.ConfigMap) {
	t.Helper()

	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 2
	})
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod1 := podFromStsTemplate(v, sts, 1)

	pods := []podState{
		{name: pod0.Name, pod: pod0, needsUpdate: true, isMaster: true, readyCondition: true, exists: true},
		{name: pod1.Name, pod: pod1, needsUpdate: false, isMaster: false, readyCondition: true, exists: true},
	}

	replicaCM := builder.BuildReplicaConfigMap(v)
	controllerRefTo(v, replicaCM)
	return v, pods, replicaCM
}

func replicaConfigMapContent(t *testing.T, c client.Client, v *vkov1.Valkey) string {
	t.Helper()
	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name:      builder.ReplicaConfigMapName(v),
		Namespace: v.Namespace,
	}, cm))
	return cm.Data[builder.ValkeyConfigKey]
}

// The replica ConfigMap must already name the promoted pod at the moment the old
// master is deleted — afterwards is too late, because the recreated pod mounts
// the ConfigMap as it exists when the kubelet starts it.
func TestHandleManualFailover_PublishesKnownMasterBeforeDeletingOldMaster(t *testing.T) {
	v, pods, replicaCM := twoReplicaFailoverFixture(t)
	promotedHost := fmt.Sprintf("test-1.test-headless.%s.svc.cluster.local", v.Namespace)

	var configAtDelete string
	captureConfigOnDelete := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if pod, ok := obj.(*corev1.Pod); ok && pod.Name == "test-0" {
				configAtDelete = replicaConfigMapContent(t, cl, v)
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, c := newInterceptedReconciler(captureConfigOnDelete, v, replicaCM, pods[0].pod, pods[1].pod)

	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}

	result := r.handleManualFailover(context.Background(), v, pods, 0)

	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.False(t, podExists(t, c, "test-0"), "the old master must be deleted")
	assert.Contains(t, configAtDelete, "replicaof "+promotedHost,
		"the replica ConfigMap must name the promoted pod before pod-0 is deleted, "+
			"otherwise the returning pod-0 elects itself master")

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, updated))
	assert.Equal(t, promotedHost, updated.Annotations[builder.AnnotationKnownMaster])
	assert.Equal(t, "test-1", updated.Annotations[annotationPromotedPod])
}

// Once pod-0 is master again the known-master pointer has to follow, otherwise a
// replica that restarts after the rolling update replicates from the pod that is
// being demoted.
func TestPromotePod0AndRedirect_ResetsKnownMasterToPod0(t *testing.T) {
	v, _, _ := twoReplicaFailoverFixture(t)
	promotedHost := fmt.Sprintf("test-1.test-headless.%s.svc.cluster.local", v.Namespace)
	masterHost := fmt.Sprintf("test-0.test-headless.%s.svc.cluster.local", v.Namespace)
	v.Annotations = map[string]string{
		builder.AnnotationKnownMaster: promotedHost,
		annotationPromotedPod:         "test-1",
	}
	replicaCM := builder.BuildReplicaConfigMap(v)
	controllerRefTo(v, replicaCM)
	require.Contains(t, replicaCM.Data[builder.ValkeyConfigKey], "replicaof "+promotedHost,
		"the fixture must start from a ConfigMap that points at the promoted pod")

	sts := stsForValkey(v)
	r, c := newTestReconciler(v, sts, replicaCM)

	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	result := r.promotePod0AndRedirect(context.Background(), v, sts, "test-0")

	require.NoError(t, result.Error)
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, updated))
	assert.Equal(t, masterHost, updated.Annotations[builder.AnnotationKnownMaster])
	assert.Contains(t, replicaConfigMapContent(t, c, v), "replicaof "+masterHost)
}

// The known-master address must not leak into the config hash: it changes during
// every failover, and a hash change would mark every pod outdated at that moment.
func TestKnownMasterAnnotation_DoesNotChangeConfigHash(t *testing.T) {
	v, _, _ := twoReplicaFailoverFixture(t)
	before := builder.ComputeConfigHash(v)

	v.Annotations = map[string]string{
		builder.AnnotationKnownMaster: fmt.Sprintf("test-1.test-headless.%s.svc.cluster.local", v.Namespace),
	}

	assert.Equal(t, before, builder.ComputeConfigHash(v),
		"publishing the promoted master must not trigger a rolling restart")
}

// While the manual failover is in flight both the old master and the promoted pod
// report master. That is not a split-brain to resolve by connected-slave count:
// with two replicas neither has a slave, the tie goes to the lowest index — the
// old master the operator just deleted — and demoting the promoted pod points the
// surviving data at a pod that is disappearing.
func TestDetectAndResolveSplitBrain_PrefersPromotedPodDuringFailover(t *testing.T) {
	v, pods, _ := twoReplicaFailoverFixture(t)
	pods[1].isMaster = true // promoted, no replicas attached yet

	r, _ := newTestReconciler(v, pods[0].pod, pods[1].pod)
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			// Neither master has a connected replica — the tie the fallback loses.
			return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
		},
	}

	resolved, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-1")

	assert.Equal(t, 1, masterIdx, "the promoted pod must be treated as the real master")
	assert.False(t, resolved[0].isMaster, "the old master must be the one demoted")
	assert.True(t, resolved[1].isMaster)
}

// The same case through the path that actually runs it: a reconcile that lands
// while the failover state is set must not send REPLICAOF to the promoted pod.
func TestHandleMultiReplicaRollingUpdate_DoesNotDemotePromotedPod(t *testing.T) {
	v, pods, replicaCM := twoReplicaFailoverFixture(t)
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateManualFailover,
		annotationPromotedPod:        "test-1",
	}
	sts := stsForValkey(v)

	r, _ := newTestReconciler(v, sts, replicaCM, pods[0].pod, pods[1].pod)

	var contacted []string
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
		contacted = append(contacted, target)
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
		},
	}

	r.handleMultiReplicaRollingUpdate(context.Background(), v, sts)

	for _, target := range contacted {
		assert.NotContains(t, target, "test-1.",
			"the promoted pod must not be demoted while the failover is in flight")
	}
}

// --- ADR 0009 D2, D3: the write that records the promotion gets a bounded conflict retry ---
//
// promoteAndRedirect runs before the three annotations are persisted, so a failed
// write leaves the cluster failed over with an empty state: the next pass hands an
// empty known master to the split-brain resolver, the two-replica tie goes to the
// lowest ordinal — the old master being deleted — and the promoted pod is demoted
// with the data on it. The dominant cause is a resourceVersion conflict against the
// concurrent status writer, and that one is recoverable inside the same pass.

// conflictOnCRUpdate fails the first n Update calls on the Valkey CR with a
// conflict, and counts every attempt.
func conflictOnCRUpdate(n int, attempts *int) interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, isCR := obj.(*vkov1.Valkey); isCR {
				*attempts++
				if *attempts <= n {
					return apierrors.NewConflict(
						schema.GroupResource{Group: vkov1.GroupVersion.Group, Resource: "valkeys"},
						obj.GetName(), fmt.Errorf("the object has been modified"))
				}
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
}

func TestHandleManualFailover_RetriesTheStateWriteOnConflict(t *testing.T) {
	v, pods, replicaCM := twoReplicaFailoverFixture(t)
	promotedHost := fmt.Sprintf("test-1.test-headless.%s.svc.cluster.local", v.Namespace)

	attempts := 0
	r, c := newInterceptedReconciler(conflictOnCRUpdate(1, &attempts), v, replicaCM, pods[0].pod, pods[1].pod)

	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}

	result := r.handleManualFailover(context.Background(), v, pods, 0)

	require.NoError(t, result.Error, "a conflict is retried, not surfaced as a failed pass")
	assert.Greater(t, attempts, 1, "the conflicting write must be retried on a fresh object")

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, updated))
	assert.Equal(t, stateManualFailover, updated.Annotations[annotationRollingUpdateState],
		"without the state the next pass does not know a failover is in flight")
	assert.Equal(t, "test-1", updated.Annotations[annotationPromotedPod])
	assert.Equal(t, promotedHost, updated.Annotations[builder.AnnotationKnownMaster],
		"the known master is the split-brain authority for the passes that follow")

	// The caller keeps using v after the retry: the ConfigMap it publishes and the
	// delete it performs both depend on the object carrying the new annotations.
	assert.Equal(t, promotedHost, v.Annotations[builder.AnnotationKnownMaster],
		"the retried object must be copied back over the caller's CR")
	assert.Contains(t, replicaConfigMapContent(t, c, v), "replicaof "+promotedHost)
	assert.False(t, podExists(t, c, "test-0"), "the old master is deleted once the state is recorded")
}

// The retry is bounded: a conflict that never clears fails the pass instead of
// spinning. The promotion has happened either way, which is the residual ADR 0009 D2, D3
// names and accepts.
func TestHandleManualFailover_StateWriteRetryIsBounded(t *testing.T) {
	v, pods, replicaCM := twoReplicaFailoverFixture(t)

	attempts := 0
	r, c := newInterceptedReconciler(conflictOnCRUpdate(1000, &attempts), v, replicaCM, pods[0].pod, pods[1].pod)

	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}

	result := r.handleManualFailover(context.Background(), v, pods, 0)

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "setting manual failover state")
	assert.Greater(t, attempts, 1, "the write is retried")
	assert.Less(t, attempts, 20, "and the retry is bounded, not a spin")
	assert.True(t, podExists(t, c, "test-0"),
		"the old master must not be deleted while the promotion is unrecorded")
}

// A non-conflict rejection is not retried: it is not going to clear by refetching,
// and the pass has to surface it.
func TestHandleManualFailover_DoesNotRetryNonConflictErrors(t *testing.T) {
	v, pods, replicaCM := twoReplicaFailoverFixture(t)

	attempts := 0
	rejectCRUpdates := interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, isCR := obj.(*vkov1.Valkey); isCR {
				attempts++
				return apierrors.NewInternalError(fmt.Errorf("admission webhook denied the request"))
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
	r, _ := newInterceptedReconciler(rejectCRUpdates, v, replicaCM, pods[0].pod, pods[1].pod)

	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}

	result := r.handleManualFailover(context.Background(), v, pods, 0)

	require.Error(t, result.Error)
	assert.Equal(t, 1, attempts, "a rejection that refetching cannot fix must not be retried")
}

// The outgoing master must not keep answering role:master for its whole
// termination window: promoteAndRedirect demotes it to a replica of the promoted
// pod, strictly after the promotion and before its deletion, so the operator does
// not produce on every failover the very state its steady-state check calls a
// split brain (ADR 0012 D9). One recording server per target pod, because the
// assertion is which command reached which pod, not only which commands ran.
func TestPromoteAndRedirect_DemotesTheOutgoingMaster(t *testing.T) {
	v, pods, replicaCM := twoReplicaFailoverFixture(t)
	promotedHost := fmt.Sprintf("test-1.test-headless.%s.svc.cluster.local", v.Namespace)
	port := int(builder.ServicePort(v))

	var mu sync.Mutex
	serverByTarget := map[string]string{}
	commandsByTarget := map[string]func() []string{}

	r, _ := newTestReconciler(v, replicaCM, pods[0].pod, pods[1].pod)
	r.NewValkeyClientFn = func(addr, _ string, _ *tls.Config) *valkeyclient.Client {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := serverByTarget[addr]; !ok {
			fakeAddr, commands := recordingValkeyServer(t)
			serverByTarget[addr] = fakeAddr
			commandsByTarget[addr] = commands
		}
		return valkeyclient.New(serverByTarget[addr])
	}

	require.NoError(t, r.promoteAndRedirect(context.Background(), v, pods, pods[1], 0, 1))

	commandsTo := func(pod string) string {
		mu.Lock()
		defer mu.Unlock()
		commands, ok := commandsByTarget[health.PodAddressForComponent(v, pod, common.ComponentValkey, port)]
		if !ok {
			return ""
		}
		return strings.Join(commands(), "\n")
	}

	promotion := strings.ToUpper(commandsTo("test-1"))
	assert.Contains(t, promotion, "REPLICAOF", "the promotion must reach the promoted pod")
	assert.Contains(t, promotion, "NO", "the promoted pod is promoted with REPLICAOF NO ONE")

	demotion := commandsTo("test-0")
	assert.Contains(t, demotion, "REPLICAOF",
		"the outgoing master must be demoted instead of answering role:master until the kubelet kills it")
	assert.Contains(t, demotion, promotedHost,
		"the demotion must point the outgoing master at the promoted pod")
}
