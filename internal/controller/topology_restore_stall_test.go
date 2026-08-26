package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// --- ADR 0010 D2-D4: Phase 1 (stateRestoringTopology) must not requeue forever ---
//
// Phase 1 waits for pod-0 to sync back from the promoted replica. Every failure it
// can hit returned a bare requeue, and the outer loop offers no escape either:
// clearStaleRollingUpdateState only runs on the updatedCount != totalPods branch,
// which topology restoration never takes. A pod-0 that never comes back therefore
// left the CR on restoring-topology indefinitely.

// topologyRestoreFixture builds a 3-replica non-sentinel cluster parked in
// stateRestoringTopology with pod-1 as the promoted master, and returns the
// reconciler, the client and the persisted StatefulSet.
func topologyRestoreFixture(t *testing.T, name string, annotations map[string]string) (*ValkeyReconciler, client.Client, *vkov1.Valkey, *appsv1.StatefulSet) {
	t.Helper()
	return topologyRestoreFixtureWithInterceptor(t, name, annotations, interceptor.Funcs{})
}

// topologyRestoreFixtureWithInterceptor is the same fixture with client
// interceptors, for the tests that make the status write fail.
func topologyRestoreFixtureWithInterceptor(t *testing.T, name string, annotations map[string]string,
	funcs interceptor.Funcs) (*ValkeyReconciler, client.Client, *vkov1.Valkey, *appsv1.StatefulSet) {
	t.Helper()

	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod1 := podFromStsTemplate(v, sts, 1)
	pod1.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod2 := podFromStsTemplate(v, sts, 2)

	r, c := newTestReconcilerWithInterceptor(funcs, v, sts, pod0, pod1, pod2)

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, v))
	if v.Annotations == nil {
		v.Annotations = map[string]string{}
	}
	for k, val := range annotations {
		v.Annotations[k] = val
	}
	require.NoError(t, c.Update(context.Background(), v))

	return r, c, v, sts
}

// pod0Unreachable makes every replication check on pod-0 fail while the other pods
// answer as healthy replicas — the "pod-0 never came back" stall.
func pod0Unreachable(name string) *mockInstanceChecker {
	return &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == name+"-0" {
				return nil, fmt.Errorf("dial tcp: connection refused")
			}
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}
}

func TestHandleTopologyRestoration_ArmsStallTimestampAndWaits(t *testing.T) {
	const name = "topo-arm"
	r, c, v, sts := topologyRestoreFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-1",
	})
	r.InstanceChecker = pod0Unreachable(name)

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue, "pod-0 may still come back, so Phase 1 keeps waiting")
	assert.False(t, result.Completed)

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, updated))
	assert.Equal(t, stateRestoringTopology, updated.Annotations[annotationRollingUpdateState],
		"the state must not move before the timeout")
	assert.NotEmpty(t, updated.Annotations[annotationTopologyRestoreStarted],
		"the wait must be timestamped, otherwise it can never be found to be stalled")
	assert.Nil(t, apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored),
		"no verdict on the topology while the wait is still legitimate")
}

// TestHandleTopologyRestoration_AbandonsAfterSyncTimeout is the ADR 0010 D2-D4 fix: once the
// wait exceeds the sync timeout, Phase 1 hands over to Phase 2 instead of requeueing
// forever. Forcing the promotion is not the escape — an unsynced pod-0 would come up
// as an empty master and discard the writes the promoted replica accepted.
func TestHandleTopologyRestoration_AbandonsAfterSyncTimeout(t *testing.T) {
	cases := []struct {
		testName string
		crName   string
		info     func(podName string) (*valkeyclient.ReplicationInfo, error)
	}{
		{
			testName: "pod-0 unreachable",
			crName:   "topo-gone",
			info: func(podName string) (*valkeyclient.ReplicationInfo, error) {
				if podName == "topo-gone-0" {
					return nil, fmt.Errorf("dial tcp: connection refused")
				}
				return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
			},
		},
		{
			testName: "pod-0 never became a replica",
			crName:   "topo-rogue",
			info: func(podName string) (*valkeyclient.ReplicationInfo, error) {
				if podName == "topo-rogue-0" {
					return &valkeyclient.ReplicationInfo{Role: "master"}, nil
				}
				return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
			},
		},
		{
			testName: "pod-0 stuck mid-sync",
			crName:   "topo-sync",
			info: func(podName string) (*valkeyclient.ReplicationInfo, error) {
				if podName == "topo-sync-0" {
					return &valkeyclient.ReplicationInfo{
						Role:                 "slave",
						MasterLinkStatus:     "up",
						MasterSyncInProgress: true,
					}, nil
				}
				return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.testName, func(t *testing.T) {
			promotedHost := fmt.Sprintf("%s-1.%s-headless.default.svc.cluster.local", tc.crName, tc.crName)
			stale := time.Now().Add(-(6 * time.Minute)).UTC().Format(time.RFC3339) // default syncTimeout is 5m

			r, c, v, sts := topologyRestoreFixture(t, tc.crName, map[string]string{
				annotationRollingUpdateState:     stateRestoringTopology,
				annotationPromotedPod:            tc.crName + "-1",
				annotationTopologyRestoreStarted: stale,
				builder.AnnotationKnownMaster:    promotedHost,
			})
			r.InstanceChecker = &mockInstanceChecker{replicationInfoFn: tc.info}

			// Any REPLICAOF sent here would be a bug: pod-0 must not be promoted.
			var contacted []string
			r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
				contacted = append(contacted, target)
				return valkeyclient.New("127.0.0.1:1")
			}

			result := r.handleTopologyRestoration(context.Background(), v, sts)

			require.Nil(t, result.Error)
			assert.True(t, result.NeedsRequeue, "Phase 2 still has to run")
			assert.Empty(t, contacted, "an unsynced pod-0 must never be promoted")

			updated := &vkov1.Valkey{}
			require.NoError(t, c.Get(context.Background(),
				types.NamespacedName{Name: tc.crName, Namespace: "default"}, updated))
			assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState],
				"the stall must escape into Phase 2, which is bounded and consolidates the masters")
			assert.Equal(t, promotedHost, updated.Annotations[builder.AnnotationKnownMaster],
				"the promoted replica stays master, so the known-master must not move to pod-0")

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored)
			require.NotNil(t, cond, "an abandoned restoration must leave a durable trace")
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Equal(t, "RestoreTimeout", cond.Reason)
		})
	}
}

// TestHandleTopologyRestoration_SuccessRecordsRestoredCondition pins the other half
// of the condition: a restoration that does hand pod-0 the master role back says so,
// so a False verdict from an earlier attempt cannot linger.
func TestHandleTopologyRestoration_SuccessRecordsRestoredCondition(t *testing.T) {
	const name = "topo-ok"
	r, c, v, sts := topologyRestoreFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-1",
	})
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}
	// Phase 1 only reaches Phase 2 when REPLICAOF NO ONE on pod-0 actually succeeds.
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	result := r.handleTopologyRestoration(context.Background(), v, sts)
	require.Nil(t, result.Error)

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, updated))
	require.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState])

	cond := apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, updated.Annotations[builder.AnnotationKnownMaster], name+"-0.",
		"pod-0 is master again, so the known-master must point back at it")
}

// TestVerifyTopologyRestored_PrefersKnownMasterOverLowestOrdinal covers the reason Phase 1 escapes
// into Phase 2 rather than into a cleared state — and the trap that comes with it. After an
// abandoned restoration the real master is the promoted replica, which in a shrunken cluster holds
// no connected slaves. A returning pod-0 that reports master ties it at zero, and the "most
// connected slaves" fallback then picks pod-0 by lowest ordinal and demotes the pod holding the
// data (docs/adr/0008-known-master-annotation-is-the-recorded-authority.md, D10, D11).
func TestVerifyTopologyRestored_PrefersKnownMasterOverLowestOrdinal(t *testing.T) {
	const name = "topo-km"
	promotedHost := fmt.Sprintf("%s-1.%s-headless.default.svc.cluster.local", name, name)

	r, _, v, sts := topologyRestoreFixture(t, name, map[string]string{
		annotationRollingUpdateState:  stateVerifyingTopology,
		annotationPromotedPod:         name + "-1",
		builder.AnnotationKnownMaster: promotedHost,
	})
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			switch podName {
			case name + "-0", name + "-1":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
			default:
				return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
			}
		},
	}

	// Both masters hold keys, so the dataset veto (ADR 0028 D1) stays inert and the
	// subject of the test remains the authority. The assertion is on the command each
	// pod received rather than on which pod was contacted: the resolver now reads a key
	// count from the master it is protecting.
	fleet := newValkeyFleet(t, r, map[string]int{
		name + "-0": 4711, name + "-1": 4711, name + "-2": 4711,
	})

	result := r.verifyTopologyRestored(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.True(t, fleet.sawReplicaOf(name+"-0"),
		"the known master holds the data; pod-0 is the rogue one")
	assert.False(t, fleet.sawReplicaOf(name+"-1"),
		"the pod the known-master annotation names must not be demoted")
}

// TestVerifyTopologyRestored_CompletesWhenPodLookupKeepsFailing bounds the one
// Phase 2 path that was unbounded too: ensureFinalizationTimestamp used to sit
// inside the rogue-master branch, so a permanently failing collectPodStates
// requeued forever, exactly like Phase 1.
func TestVerifyTopologyRestored_CompletesWhenPodLookupKeepsFailing(t *testing.T) {
	const name = "topo-blind"

	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})
	sts := stsForValkey(v)
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateVerifyingTopology,
		annotationPromotedPod:        name + "-1",
	}

	failPodGets := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, isPod := obj.(*corev1.Pod); isPod {
				return apierrors.NewInternalError(fmt.Errorf("apiserver unavailable"))
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	r, c := newInterceptedReconciler(failPodGets, v, sts)

	// First pass: the wait is legitimate and only gets timestamped.
	result := r.verifyTopologyRestored(context.Background(), v, sts)
	require.Nil(t, result.Error)
	require.True(t, result.NeedsRequeue)

	current := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, current))
	require.NotEmpty(t, current.Annotations[annotationFinalizationTimestamp],
		"the failing verification must be timestamped")

	// Second pass, with the budget spent: the update completes rather than stalling.
	current.Annotations[annotationFinalizationTimestamp] =
		time.Now().Add(-(finalizationStallTimeout + time.Minute)).UTC().Format(time.RFC3339)
	require.NoError(t, c.Update(context.Background(), current))

	result = r.verifyTopologyRestored(context.Background(), current, sts)
	require.Nil(t, result.Error)
	assert.True(t, result.Completed, "a verification that can never run must not block the update forever")

	final := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, final))
	assert.Empty(t, final.Annotations[annotationRollingUpdateState], "all rolling update state must be cleared")
	assert.Empty(t, final.Annotations[annotationTopologyRestoreStarted])
}

func TestKnownMasterPodName(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		want        string
	}{
		{name: "no annotations", annotations: nil, want: ""},
		{name: "unset", annotations: map[string]string{}, want: ""},
		{
			name:        "fqdn is reduced to the pod name",
			annotations: map[string]string{builder.AnnotationKnownMaster: "vk-1.vk-headless.default.svc.cluster.local"},
			want:        "vk-1",
		},
		{
			name:        "bare pod name passes through",
			annotations: map[string]string{builder.AnnotationKnownMaster: "vk-1"},
			want:        "vk-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &vkov1.Valkey{}
			v.Annotations = tc.annotations
			assert.Equal(t, tc.want, knownMasterPodName(v))
		})
	}
}

// --- ADR 0010 D3: the verdict is written before the state moves ---
//
// TopologyRestored is not a steady-state report that the next pass recomputes: both
// writers run exactly once per rolling update and then enter stateVerifyingTopology,
// which no pass leaves back into Phase 1. Swallowing the write therefore lost the
// verdict for the life of the cluster -- observed in CI as
// TestE2E_RollingUpdate_TopologyRestoreAbandoned timing out on a condition the
// operator had already given up on writing, while the abandon itself had happened.

// valkeyConflict is the error the API server returns for a status update carrying a
// resourceVersion older than the stored one.
func valkeyConflict(name string) error {
	return apierrors.NewConflict(
		vkov1.GroupVersion.WithResource("valkeys").GroupResource(), name, assert.AnError)
}

// failingStatusUpdates fails the first n status updates of a Valkey with err and
// lets every later one through; n < 0 fails all of them. The returned counter
// reports how many status updates were attempted, which is what distinguishes a
// retried write from a single-shot one.
func failingStatusUpdates(n int, err error) (interceptor.Funcs, *int) {
	attempts := 0
	return interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, cl client.Client, subResource string,
			obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if _, isValkey := obj.(*vkov1.Valkey); isValkey && subResource == "status" {
				attempts++
				if n < 0 || attempts <= n {
					return err
				}
			}
			return cl.SubResource(subResource).Update(ctx, obj, opts...)
		},
	}, &attempts
}

// stalledAbandonFixture parks a CR in Phase 1 with an expired budget and pod-0
// unreachable, which is the state abandonTopologyRestoration is entered from.
func stalledAbandonFixture(t *testing.T, name string, funcs interceptor.Funcs) (
	*ValkeyReconciler, client.Client, *vkov1.Valkey, *appsv1.StatefulSet) {
	t.Helper()

	r, c, v, sts := topologyRestoreFixtureWithInterceptor(t, name, map[string]string{
		annotationRollingUpdateState:     stateRestoringTopology,
		annotationPromotedPod:            name + "-1",
		annotationTopologyRestoreStarted: time.Now().Add(-6 * time.Minute).UTC().Format(time.RFC3339),
		builder.AnnotationKnownMaster: fmt.Sprintf(
			"%s-1.%s-headless.default.svc.cluster.local", name, name),
	}, funcs)
	r.InstanceChecker = pod0Unreachable(name)
	return r, c, v, sts
}

// TestAbandonTopologyRestoration_ConflictHoldsPhase1 pins the ordering: a verdict
// that could not be recorded must not be followed by the state transition that makes
// it unwritable. The pass fails instead, and the work queue brings it back to a CR
// still in stateRestoringTopology and still stalled.
func TestAbandonTopologyRestoration_ConflictHoldsPhase1(t *testing.T) {
	const name = "topo-conflict"
	funcs, attempts := failingStatusUpdates(-1, valkeyConflict(name))
	r, c, v, sts := stalledAbandonFixture(t, name, funcs)

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	require.Error(t, result.Error)
	assert.True(t, apierrors.IsConflict(result.Error),
		"the conflict is what tells the caller another pass can still record the verdict")
	assert.Greater(t, *attempts, 1,
		"a conflicting status write is retried against a freshly read CR before the pass gives up")

	updated := crGet(t, c, name)
	assert.Equal(t, stateRestoringTopology, updated.Annotations[annotationRollingUpdateState],
		"the state must not move while the verdict is unrecorded, or no pass can ever write it")
	assert.Empty(t, updated.Annotations[annotationFinalizationTimestamp],
		"Phase 2 has not started, so its budget must not be spent")
	assert.Nil(t, apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored))
}

// TestAbandonTopologyRestoration_ConflictRetriedThenRecorded is the case CI hit: the
// operator raced its own preceding update through the manager cache, so the first
// status write was rejected with a stale resourceVersion. One retry is enough.
func TestAbandonTopologyRestoration_ConflictRetriedThenRecorded(t *testing.T) {
	const name = "topo-retry"
	funcs, attempts := failingStatusUpdates(1, valkeyConflict(name))
	r, c, v, sts := stalledAbandonFixture(t, name, funcs)

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue, "Phase 2 still has to run")
	assert.Equal(t, 2, *attempts, "the write is retried once and then lands")

	updated := crGet(t, c, name)
	assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState])
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored)
	require.NotNil(t, cond, "a conflict that clears must not cost the durable trace")
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "RestoreTimeout", cond.Reason)
}

// TestAbandonTopologyRestoration_PermanentFailureStillEscapes is the other side of
// the same rule. A status write that fails identically on every pass -- a withdrawn
// RBAC on the status subresource is the realistic one -- must not hold the rolling
// update in Phase 1: that would trade a lost condition for the unbounded wait ADR
// 0010 D2-D4 exists to remove. The record is dropped, the escape happens.
func TestAbandonTopologyRestoration_PermanentFailureStillEscapes(t *testing.T) {
	const name = "topo-forbidden"
	funcs, _ := failingStatusUpdates(-1, apierrors.NewForbidden(
		vkov1.GroupVersion.WithResource("valkeys").GroupResource(), name, assert.AnError))
	r, c, v, sts := stalledAbandonFixture(t, name, funcs)

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	require.Nil(t, result.Error, "a failure no pass can fix must not be retried forever")
	assert.True(t, result.NeedsRequeue)

	updated := crGet(t, c, name)
	assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState],
		"Phase 2 consolidates the masters, and it is the last pass that can")
	assert.Nil(t, apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored),
		"the verdict is lost, which is the accepted cost of keeping the bound")
}

// TestPromotePod0AndRedirect_ConflictHoldsPhase1 covers the successful verdict, whose
// writer is equally one-shot. Requeueing without advancing is free here: pod-0 is
// already master, the known-master annotation already names it, and REPLICAOF NO ONE
// on the next pass is a no-op.
func TestPromotePod0AndRedirect_ConflictHoldsPhase1(t *testing.T) {
	const name = "topo-ok-conflict"
	funcs, attempts := failingStatusUpdates(-1, valkeyConflict(name))
	r, c, v, sts := topologyRestoreFixtureWithInterceptor(t, name, map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-1",
	}, funcs)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	result := r.promotePod0AndRedirect(context.Background(), v, sts, name+"-0")

	require.Error(t, result.Error)
	assert.True(t, apierrors.IsConflict(result.Error))
	assert.Greater(t, *attempts, 1)

	updated := crGet(t, c, name)
	assert.Equal(t, stateRestoringTopology, updated.Annotations[annotationRollingUpdateState],
		"without the record the pass must be repeatable, which stateVerifyingTopology is not")
	assert.Contains(t, updated.Annotations[builder.AnnotationKnownMaster], name+"-0.",
		"the promotion itself was recorded, so the retry finds a topology that matches the annotation")
}
