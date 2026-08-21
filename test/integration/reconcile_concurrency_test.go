//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/controller"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// slowProbeAnnotation marks the one CR whose data-plane probes the suite delays.
// Every other CR is delegated to the real Checker, so the rest of the suite sees
// exactly the behaviour it saw before this file existed.
const slowProbeAnnotation = "test.vko.gtrfc.com/slow-probe"

// slowProbeDelay is how long a marked CR holds its reconcile worker. It stands in
// for a cluster whose pods are Running but do not answer: the real cost is
// replicas x the 5 s client timeout, and the number here only has to be long
// enough that a second CR waiting behind it is unmistakable.
const slowProbeDelay = 8 * time.Second

// slowProbeChecker is the InstanceChecker the suite installs. For a CR carrying
// slowProbeAnnotation it blocks for slowProbeDelay and reports the probe as
// failed; for every other CR it delegates.
//
// It is armed by the concurrency test and disarmed again in its cleanup, so no
// other test in this package pays the delay.
type slowProbeChecker struct {
	delegate controller.InstanceChecker

	mu      sync.Mutex
	armed   bool
	entered chan struct{}
}

// arm makes the stub delay marked CRs and returns a channel that is closed as
// soon as the first delayed probe has begun — the moment a reconcile worker is
// provably occupied.
func (s *slowProbeChecker) arm() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = true
	s.entered = make(chan struct{})
	return s.entered
}

func (s *slowProbeChecker) disarm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = false
}

// stall reports whether v must be delayed, and signals the first entry.
func (s *slowProbeChecker) stall(v *vkov1.Valkey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.armed || v.Annotations[slowProbeAnnotation] != "true" {
		return false
	}
	select {
	case <-s.entered:
	default:
		close(s.entered)
	}
	return true
}

func (s *slowProbeChecker) PingPod(ctx context.Context, v *vkov1.Valkey, podName string) error {
	if s.stall(v) {
		time.Sleep(slowProbeDelay)
		return context.DeadlineExceeded
	}
	return s.delegate.PingPod(ctx, v, podName)
}

func (s *slowProbeChecker) CheckCluster(ctx context.Context, v *vkov1.Valkey) *health.ClusterState {
	if s.stall(v) {
		time.Sleep(slowProbeDelay)
		return &health.ClusterState{Error: context.DeadlineExceeded}
	}
	return s.delegate.CheckCluster(ctx, v)
}

func (s *slowProbeChecker) GetReplicationInfo(
	ctx context.Context, v *vkov1.Valkey, podName string,
) (*valkeyclient.ReplicationInfo, error) {
	if s.stall(v) {
		time.Sleep(slowProbeDelay)
		return nil, context.DeadlineExceeded
	}
	return s.delegate.GetReplicationInfo(ctx, v, podName)
}

// TestReconcileConcurrency_StuckClusterDoesNotBlockOthers is the behavioural half
// of ADR 0019. The wiring half — that the option carries a worker count above one
// — is a unit test; only a running manager shows what the worker count buys.
//
// Shape: one CR is driven into a reconcile pass that blocks for slowProbeDelay
// inside the data-plane probe, and only once that pass is provably running does
// the second CR get created. With controller-runtime's default of one worker the
// second CR cannot be looked at before the first pass returns, so its StatefulSet
// appears no earlier than slowProbeDelay later. The assertion is that it appears
// within a small fraction of that.
//
// Why the marked CR needs its StatefulSet status patched first: envtest runs no
// kubelet and no StatefulSet controller, so ready replicas stay at zero and
// updateStatus never reaches the connectivity check that the stub intercepts.
func TestReconcileConcurrency_StuckClusterDoesNotBlockOthers(t *testing.T) {
	ctx := testCtx

	// How long the second CR may take to get its first pass. Far below
	// slowProbeDelay, far above what a free worker needs in envtest.
	const freeWorkerBudget = 3 * time.Second

	stuck := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "concurrency-stuck",
			Namespace:   "default",
			Annotations: map[string]string{slowProbeAnnotation: "true"},
		},
		Spec: vkov1.ValkeySpec{Replicas: 1, Image: "valkey/valkey:8.0"},
	}
	require.NoError(t, k8sClient.Create(ctx, stuck))
	t.Cleanup(func() {
		slowProbes.disarm()
		_ = k8sClient.Delete(context.Background(), stuck)
	})

	stuckSts := waitForStatefulSet(t, "concurrency-stuck", 30*time.Second)

	entered := slowProbes.arm()

	// Report the StatefulSet as fully ready so the pass reaches the connectivity
	// check. The status write also re-enqueues the CR through the Owns watch.
	stuckSts.Status.Replicas = 1
	stuckSts.Status.ReadyReplicas = 1
	stuckSts.Status.AvailableReplicas = 1
	stuckSts.Status.CurrentReplicas = 1
	stuckSts.Status.UpdatedReplicas = 1
	stuckSts.Status.ObservedGeneration = stuckSts.Generation
	require.NoError(t, k8sClient.Status().Update(ctx, stuckSts))

	select {
	case <-entered:
	case <-time.After(60 * time.Second):
		t.Fatal("the marked CR never reached the data-plane probe, so no worker was ever occupied " +
			"and this test would prove nothing")
	}

	// A worker is now blocked for slowProbeDelay. Everything below happens inside
	// that window.
	healthy := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "concurrency-healthy", Namespace: "default"},
		Spec:       vkov1.ValkeySpec{Replicas: 1, Image: "valkey/valkey:8.0"},
	}
	start := time.Now()
	require.NoError(t, k8sClient.Create(ctx, healthy))
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), healthy) })

	waitForStatefulSet(t, "concurrency-healthy", slowProbeDelay)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, freeWorkerBudget,
		"a second Valkey CR waited %v for its first reconcile while one cluster was stuck in a probe; "+
			"with a single worker every CR in the fleet inherits that latency", elapsed)
}

// waitForStatefulSet polls until the data StatefulSet of name exists and returns
// it, or fails the test.
func waitForStatefulSet(t *testing.T, name string, timeout time.Duration) *appsv1.StatefulSet {
	t.Helper()

	sts := &appsv1.StatefulSet{}
	key := types.NamespacedName{Name: name, Namespace: "default"}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if err := k8sClient.Get(testCtx, key, sts); err == nil {
			return sts
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("StatefulSet %s did not appear within %v", name, timeout)
	return nil
}
