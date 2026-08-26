package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// nonEmptyKeyCount is any DBSIZE above zero: the veto keys on empty-versus-not, never
// on the size of the dataset.
const nonEmptyKeyCount = 4711

// The tests in this file pin ADR 0028: inside a rolling update the split-brain
// resolver may not demote a master that holds data toward an authority that holds
// none, and the drain stamp is the one piece of evidence that still outranks the
// recorded authority there.

// --- fixtures -------------------------------------------------------------------

// valkeyFleet is one fake Valkey server per pod. It answers DBSIZE with that pod's
// configured key count and records every command the pod received, so a test can
// assert which pod was sent a REPLICAOF rather than which pod was merely contacted --
// the resolver now reads a key count from the pod it is protecting, so "contacted at
// all" no longer distinguishes a demotion from a probe.
type valkeyFleet struct {
	mu       sync.Mutex
	commands map[string][]string
}

func newValkeyFleet(t *testing.T, r *ValkeyReconciler, keysByPod map[string]int) *valkeyFleet {
	t.Helper()

	f := &valkeyFleet{commands: map[string][]string{}}
	addrs := map[string]string{}
	for pod, keys := range keysByPod {
		addrs[pod] = f.listen(t, pod, keys)
	}

	r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
		host, _, _ := net.SplitHostPort(target)
		pod := host
		if idx := strings.Index(host, "."); idx > 0 {
			pod = host[:idx]
		}
		addr, ok := addrs[pod]
		if !ok {
			// No server for this pod: an unreachable pod, which is what a test that
			// omits a pod from keysByPod is asking for.
			return valkeyclient.New("127.0.0.1:1")
		}
		return valkeyclient.New(addr)
	}
	return f
}

func (f *valkeyFleet) listen(t *testing.T, pod string, keys int) string {
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
				request := strings.ToUpper(string(buf[:n]))
				f.record(pod, request)
				switch {
				case strings.Contains(request, "WAIT"):
					_, _ = conn.Write([]byte(":1\r\n"))
				case strings.Contains(request, "DBSIZE"):
					_, _ = fmt.Fprintf(conn, ":%d\r\n", keys)
				default:
					_, _ = conn.Write([]byte("+OK\r\n"))
				}
			}()
		}
	}()

	return ln.Addr().String()
}

func (f *valkeyFleet) record(pod, request string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[pod] = append(f.commands[pod], request)
}

// sawReplicaOf reports whether the pod was sent a REPLICAOF, i.e. whether it was
// demoted. Polled rather than read once: demoteRogueMaster writes on its own
// connection and the server goroutine records it asynchronously.
func (f *valkeyFleet) sawReplicaOf(pod string) bool {
	for i := 0; i < 50; i++ {
		f.mu.Lock()
		for _, cmd := range f.commands[pod] {
			if strings.Contains(cmd, "REPLICAOF") {
				f.mu.Unlock()
				return true
			}
		}
		f.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// withReachableValkey points every Valkey client of r at one fake server answering
// DBSIZE with keys. Tests that predate the dataset veto use it so their subject stays
// the resolution they were written for: a non-empty authority never triggers the veto.
func withReachableValkey(t *testing.T, r *ValkeyReconciler) {
	t.Helper()
	addr := fakeValkeyServerWithKeys(t, nonEmptyKeyCount)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
}

// rollSplitBrainFixture builds a three-replica non-Sentinel cluster in which pod-0 and
// pod-2 both report master, and returns the CR, its StatefulSet and the pod states.
//
// Pod-0 is deliberately one of the two: it is the ordinal the init script hands the
// master config to unconditionally, so it is the pod a stale record resurrects as an
// empty master (docs/adr/0028-a-demotion-may-not-discard-the-only-dataset.md). Pod-2 is
// the drain target, since buildReplicaAddrs walks the ordinals ascending.
func rollSplitBrainFixture(t *testing.T) (*vkov1.Valkey, *appsv1.StatefulSet, []podState) {
	t.Helper()

	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	sts := stsForValkey(v)

	pods := make([]podState, 3)
	for i := 0; i < 3; i++ {
		pod := createPodForSts(v, i, "valkey/valkey:8.0", true)
		pods[i] = podState{
			name: pod.Name, pod: pod, exists: true, readyCondition: true,
			isMaster: i == 0 || i == 2,
		}
	}
	return v, sts, pods
}

func stampPod(pod *corev1.Pod, at time.Time) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[common.AnnotationDrainPromotedAt] = at.UTC().Format(time.RFC3339)
}

func knownMasterHost(v *vkov1.Valkey, pod string) string {
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local", pod,
		common.HeadlessServiceName(v, common.ComponentValkey), v.Namespace)
}

// --- ADR 0028 D1: the dataset veto -----------------------------------------------

func TestDetectAndResolveSplitBrain_RefusesToDemoteADataHolderTowardAnEmptyAuthority(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: knownMasterHost(v, "test-0")}

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": 0, "test-2": nonEmptyKeyCount})

	result, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.Equal(t, 0, masterIdx, "the authority still names the real master for the caller")
	assert.True(t, result[2].isMaster,
		"the pod holding the data keeps the master role: demoting it would discard the only dataset")
	assert.False(t, fleet.sawReplicaOf("test-2"), "no REPLICAOF may reach the pod holding the data")
}

func TestDetectAndResolveSplitBrain_DemotesWhenTheAuthorityHoldsData(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": nonEmptyKeyCount, "test-2": nonEmptyKeyCount})

	result, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.Equal(t, 0, masterIdx)
	assert.False(t, result[2].isMaster, "an authority that holds data resolves the split brain as before")
	assert.True(t, fleet.sawReplicaOf("test-2"))
}

func TestDetectAndResolveSplitBrain_DemotesWhenBothMastersAreEmpty(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": 0, "test-2": 0})

	result, _ := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.False(t, result[2].isMaster,
		"an empty cluster is a legitimate state and must not stall its own resolution")
	assert.True(t, fleet.sawReplicaOf("test-2"))
}

func TestDetectAndResolveSplitBrain_RefusesWhenAKeyCountIsUnreadable(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	// No server for either pod: every DBSIZE is an unanswered question, and ADR 0028 D3
	// makes that a refusal rather than a demotion.
	fleet := newValkeyFleet(t, r, map[string]int{})

	result, _ := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.True(t, result[2].isMaster, "an unreadable key count fails closed toward not demoting")
	assert.False(t, fleet.sawReplicaOf("test-2"))
}

func TestDetectAndResolveSplitBrain_TheVetoAlsoGuardsTheConnectedSlavesTiebreak(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	// Both masters tie at zero connected slaves, so the tiebreak picks the lowest
	// ordinal -- the empty pod-0 (ADR 0011 D3). The veto is what keeps that from
	// costing the dataset.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
		},
	}
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": 0, "test-2": nonEmptyKeyCount})

	result, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "")

	assert.Equal(t, 0, masterIdx, "the tiebreak still ties at zero and picks the lowest ordinal")
	assert.True(t, result[2].isMaster, "and the veto refuses the demotion it would have caused")
	assert.False(t, fleet.sawReplicaOf("test-2"))
}

func TestDetectAndResolveSplitBrain_RefusalEmitsNoEvent(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	rec := &fakeEventRecorder{}
	r.Recorder = rec
	newValkeyFleet(t, r, map[string]int{"test-0": 0, "test-2": nonEmptyKeyCount})

	r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.Empty(t, rec.events,
		"the resolver reports nothing; MultipleMasters carries the level and SplitBrainDetected the edge")
}

// --- ADR 0028 D2: the drain stamp outranks the recorded authority ------------------

func TestDetectAndResolveSplitBrain_AdoptsTheDrainStampedMaster(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: knownMasterHost(v, "test-0")}
	stampPod(pods[2].pod, time.Now())

	r, c := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": 0, "test-2": nonEmptyKeyCount})

	result, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.Equal(t, 2, masterIdx, "the stamped pod is the master a promoter nobody recorded produced")
	assert.False(t, result[0].isMaster, "the empty recorded master is the one demoted")
	assert.True(t, fleet.sawReplicaOf("test-0"))

	fresh := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, fresh))
	assert.Equal(t, knownMasterHost(v, "test-2"), fresh.Annotations[builder.AnnotationKnownMaster],
		"the adoption is recorded before anything is demoted (ADR 0009)")

	stamped := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "test-2", Namespace: v.Namespace}, stamped))
	assert.NotContains(t, stamped.Annotations, common.AnnotationDrainPromotedAt,
		"the stamp is spent evidence once the promotion is recorded (ADR 0011 D16)")
}

func TestDetectAndResolveSplitBrain_RefusesWhenTwoMastersCarryAStamp(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: knownMasterHost(v, "test-0")}
	stampPod(pods[0].pod, time.Now())
	stampPod(pods[2].pod, time.Now())

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": nonEmptyKeyCount, "test-2": nonEmptyKeyCount})

	result, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.Equal(t, 0, masterIdx, "the caller keeps the master index it came in with")
	assert.True(t, result[0].isMaster)
	assert.True(t, result[2].isMaster, "ambiguous evidence ends the resolution (ADR 0011 D10)")
	assert.False(t, fleet.sawReplicaOf("test-0"))
	assert.False(t, fleet.sawReplicaOf("test-2"))
}

func TestDetectAndResolveSplitBrain_IgnoresAStampOnAForeignPod(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: knownMasterHost(v, "test-0")}
	stampPod(pods[2].pod, time.Now())
	// A pod this cluster's StatefulSet did not create proves nothing (ADR 0020 D9).
	pods[2].pod.OwnerReferences = nil

	r, _ := newTestReconciler(v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": nonEmptyKeyCount, "test-2": nonEmptyKeyCount})

	_, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.Equal(t, 0, masterIdx, "the recorded authority decides; a foreign stamp is not evidence")
	assert.True(t, fleet.sawReplicaOf("test-2"))
}

func TestDetectAndResolveSplitBrain_ResolvesNothingWhenTheAdoptionCannotBeRecorded(t *testing.T) {
	v, sts, pods := rollSplitBrainFixture(t)
	v.Annotations = map[string]string{builder.AnnotationKnownMaster: knownMasterHost(v, "test-0")}
	stampPod(pods[2].pod, time.Now())

	deny := interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, isCR := obj.(*vkov1.Valkey); isCR {
				return apierrors.NewForbidden(
					schema.GroupResource{Group: vkov1.GroupVersion.Group, Resource: "valkeys"},
					obj.GetName(), fmt.Errorf("denied"))
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
	r, _ := newTestReconcilerWithInterceptor(deny, v, sts, pods[0].pod, pods[1].pod, pods[2].pod)
	fleet := newValkeyFleet(t, r, map[string]int{"test-0": nonEmptyKeyCount, "test-2": nonEmptyKeyCount})

	result, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 0, "test-0")

	assert.Equal(t, 0, masterIdx)
	assert.True(t, result[0].isMaster)
	assert.True(t, result[2].isMaster,
		"an unrecorded promotion is not an authority, so nothing is demoted toward it")
	assert.False(t, fleet.sawReplicaOf("test-0"))
	assert.False(t, fleet.sawReplicaOf("test-2"))
}

// --- the companion fix: the direct known-master write clears the stamps ------------

func TestPersistManualFailoverState_ClearsTheDrainStamps(t *testing.T) {
	v, pods, replicaCM := twoReplicaFailoverFixture(t)
	stampPod(pods[0].pod, time.Now())
	sts := stsForValkey(v)

	r, c := newTestReconciler(v, sts, replicaCM, pods[0].pod, pods[1].pod)

	require.NoError(t, r.persistManualFailoverState(context.Background(), v,
		"test-1", knownMasterHost(v, "test-1")))

	stale := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "test-0", Namespace: v.Namespace}, stale))
	assert.NotContains(t, stale.Annotations, common.AnnotationDrainPromotedAt,
		"this write records a promotion, so every stamp of the cluster is spent evidence")
}
