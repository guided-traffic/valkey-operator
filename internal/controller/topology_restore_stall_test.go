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

// --- NA23: Phase 1 (stateRestoringTopology) must not requeue forever ---
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

	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod1 := podFromStsTemplate(v, sts, 1)
	pod1.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod2 := podFromStsTemplate(v, sts, 2)

	r, c := newTestReconciler(v, sts, pod0, pod1, pod2)

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

// TestHandleTopologyRestoration_AbandonsAfterSyncTimeout is the NA23 fix: once the
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

// TestVerifyTopologyRestored_PrefersKnownMasterOverLowestOrdinal covers the reason
// Phase 1 escapes into Phase 2 rather than into a cleared state — and the trap that
// comes with it. After an abandoned restoration the real master is the promoted
// replica, which in a shrunken cluster holds no connected slaves. A returning pod-0
// that reports master ties it at zero, and the "most connected slaves" fallback then
// picks pod-0 by lowest ordinal and demotes the pod holding the data (NA21).
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

	addr := fakeValkeyServer(t)
	var demoted []string
	r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
		demoted = append(demoted, target)
		return valkeyclient.New(addr)
	}

	result := r.verifyTopologyRestored(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	require.Len(t, demoted, 1, "exactly one of the two masters may be demoted")
	assert.Contains(t, demoted[0], name+"-0.",
		"the known master holds the data; pod-0 is the rogue one")
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
