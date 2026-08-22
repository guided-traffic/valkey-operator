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

// The tests in this file pin the two ways the Phase 1 bound of the topology
// restoration could fail to do its job, plus the guard that decides whether the
// returning pod-0 is the new pod at all:
//
//   - ADR 0010 D7, D8: the bound is armed by an annotation write whose error was discarded.
//     A CR write that keeps failing left the bound unarmed forever, so the phase it
//     bounds requeued forever — the stall the bound exists to break.
//   - ADR 0010 D10: the bound was only set when absent, so a timestamp left behind by a
//     rolling update that died in restoring-topology spent the budget of the next
//     one before it started.
//   - ADR 0007 D4: the "is this the new pod" guard compared the image only, which a
//     config-hash-only rolling update passes trivially.

// testNamespace is the namespace every fixture in this file lives in.
const testNamespace = "default"

// crGet reads the CR back from the API. It is the only CR reader in the package tests:
// status_phase_test.go carried storedValkey and condition_generation_test.go an inline copy inside
// conditionOf, both folded onto this one (docs/adr/0010-every-rolling-update-wait-is-bounded.md,
// D14). Every fixture in the package lives in testNamespace, which is why the namespace is not a
// parameter.
func crGet(t *testing.T, c client.Client, name string) *vkov1.Valkey {
	t.Helper()
	got := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: testNamespace}, got))
	return got
}

// rewindWaitBound moves the in-memory arming time of one bound into the past, so
// a test can reach a deadline without waiting for it. The annotation copy is not
// touched — the point of these tests is what happens when it never landed.
func rewindWaitBound(t *testing.T, r *ValkeyReconciler, name, bound string, age time.Duration) {
	t.Helper()
	key := waitBoundKey(testNamespace, name, bound)
	r.nudges.mu.Lock()
	defer r.nudges.mu.Unlock()
	_, armed := r.nudges.first[key]
	require.True(t, armed, "the bound must be armed in memory before it can be rewound")
	r.nudges.first[key] = time.Now().Add(-age)
}

func waitBoundArmed(r *ValkeyReconciler, name, bound string) bool {
	_, armed := r.nudges.firstSeen(waitBoundKey(testNamespace, name, bound))
	return armed
}

// multiReplicaFixture builds a three-pod non-sentinel cluster whose pods all match
// the persisted StatefulSet template, and returns the reconciler, its client, the
// CR as stored, and the StatefulSet.
func multiReplicaFixture(t *testing.T, name string, annotations map[string]string,
	funcs *interceptor.Funcs) (*ValkeyReconciler, client.Client, *vkov1.Valkey, *appsv1.StatefulSet) {
	t.Helper()

	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	v.Annotations = annotations
	sts := stsForValkey(v)
	pods := []client.Object{
		podFromStsTemplate(v, sts, 0),
		podFromStsTemplate(v, sts, 1),
		podFromStsTemplate(v, sts, 2),
	}
	objs := append([]client.Object{v, sts}, pods...)

	var r *ValkeyReconciler
	var c client.Client
	if funcs != nil {
		r, c = newInterceptedReconciler(*funcs, objs...)
	} else {
		r, c = newTestReconciler(objs...)
	}

	return r, c, crGet(t, c, name), sts
}

// --- ADR 0010 D10: entering Phase 1 must restart its budget ---

// A rolling update that died in restoring-topology leaves topology-restore-started
// behind, and clearStaleRollingUpdateState only fires when nothing has been replaced
// yet. The next update therefore reached Phase 1 with an hours-old timestamp and
// abandoned the restoration on the first pass, without ever attempting one.
func TestHandlePostManualFailover_ReArmsPhase1BudgetOnEntry(t *testing.T) {
	const name = "rearm"
	stale := time.Now().Add(-4 * time.Hour).UTC().Format(time.RFC3339)

	r, c, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState:     stateManualFailover,
		annotationPromotedPod:            name + "-1",
		annotationTopologyRestoreStarted: stale,
	}, nil)

	// REPLICAOF on the returning pod-0 has to succeed for the state to advance.
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	result := r.handlePostManualFailover(context.Background(), v, sts)
	require.Nil(t, result.Error)
	require.True(t, result.NeedsRequeue)

	updated := crGet(t, c, name)
	require.Equal(t, stateRestoringTopology, updated.Annotations[annotationRollingUpdateState])

	started, err := time.Parse(time.RFC3339, updated.Annotations[annotationTopologyRestoreStarted])
	require.NoError(t, err, "Phase 1 must be timestamped when it is entered")
	assert.WithinDuration(t, time.Now(), started, time.Minute,
		"the budget of a previous, died rolling update must not be inherited")
	assert.True(t, waitBoundArmed(r, name, boundTopologyRestore),
		"the in-memory copy of the bound is re-armed at the same point")

	// The consequence, which is what the operator actually gets wrong without the
	// re-arm: the first Phase 1 pass waits for pod-0 instead of giving up on it.
	r.InstanceChecker = pod0Unreachable(name)
	result = r.handleTopologyRestoration(context.Background(), v, sts)
	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)

	current := crGet(t, c, name)
	assert.Equal(t, stateRestoringTopology, current.Annotations[annotationRollingUpdateState],
		"a restoration that just started must not be abandoned before it was attempted")
	assert.Nil(t, apimeta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeTopologyRestored),
		"no TopologyRestoreAbandoned verdict on the first pass of a fresh budget")
}

// --- ADR 0010 D7, D8: the bound must survive a CR write that never lands ---

// rejectTopologyRestoreArming refuses exactly the write that arms Phase 1 — an
// admission webhook on the CR, or any other permanent rejection. The abandon write
// that follows carries a different state and passes.
func rejectTopologyRestoreArming(attempts *int) interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			cr, isCR := obj.(*vkov1.Valkey)
			if isCR && cr.Annotations[annotationRollingUpdateState] == stateRestoringTopology &&
				cr.Annotations[annotationTopologyRestoreStarted] != "" {
				*attempts++
				return apierrors.NewInternalError(fmt.Errorf("admission webhook denied the annotation"))
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
}

func TestWaitOrAbandonTopologyRestoration_BoundHoldsWhenArmingWriteFails(t *testing.T) {
	const name = "arm-blocked"
	promotedHost := fmt.Sprintf("%s-1.%s-headless.default.svc.cluster.local", name, name)

	attempts := 0
	funcs := rejectTopologyRestoreArming(&attempts)
	r, c, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState:  stateRestoringTopology,
		annotationPromotedPod:         name + "-1",
		builder.AnnotationKnownMaster: promotedHost,
	}, &funcs)
	r.InstanceChecker = pod0Unreachable(name)

	// Pass 1: the wait is legitimate, and arming it fails.
	result := r.handleTopologyRestoration(context.Background(), v, sts)
	require.Nil(t, result.Error)
	require.True(t, result.NeedsRequeue)
	require.Positive(t, attempts, "the arming write must have been attempted")

	require.Empty(t, crGet(t, c, name).Annotations[annotationTopologyRestoreStarted],
		"the fixture only works while the annotation truly cannot be persisted")
	require.True(t, waitBoundArmed(r, name, boundTopologyRestore),
		"a rejected annotation write must still leave the bound armed in memory")

	// Pass 2 works on a freshly read CR, exactly as the next reconcile does — the
	// annotation is still absent, so only the in-memory bound can end this wait.
	rewindWaitBound(t, r, name, boundTopologyRestore, 6*time.Minute) // syncTimeout is 5m
	next := crGet(t, c, name)

	result = r.handleTopologyRestoration(context.Background(), next, sts)
	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue, "Phase 2 still has to run")

	final := crGet(t, c, name)
	assert.Equal(t, stateVerifyingTopology, final.Annotations[annotationRollingUpdateState],
		"the bound must fire from memory when its annotation can never be written")
	assert.Equal(t, promotedHost, final.Annotations[builder.AnnotationKnownMaster],
		"an abandoned restoration leaves the promoted replica as master")

	cond := apimeta.FindStatusCondition(final.Status.Conditions, vkov1.ConditionTypeTopologyRestored)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "RestoreTimeout", cond.Reason)
}

// ensureWaitBound must not leave the annotation on the object it failed to write:
// the next pass would re-arm it in memory, and the always-fresh value would shadow
// the tracker that is supposed to carry the deadline.
func TestEnsureWaitBound_DropsTheAnnotationItCouldNotPersist(t *testing.T) {
	const name = "arm-drop"
	attempts := 0
	funcs := rejectTopologyRestoreArming(&attempts)
	r, _, v, _ := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-1",
	}, &funcs)

	r.ensureTopologyRestoreTimestamp(context.Background(), v)

	require.Equal(t, 1, attempts)
	assert.Empty(t, v.Annotations[annotationTopologyRestoreStarted],
		"the in-memory object must reflect what was persisted, so the tracker stays authoritative")
	assert.True(t, waitBoundArmed(r, name, boundTopologyRestore))

	// With the annotation gone, the rewound in-memory bound is what answers.
	rewindWaitBound(t, r, name, boundTopologyRestore, 6*time.Minute)
	assert.True(t, r.isTopologyRestoreStalled(v))
}

// --- ADR 0010 D7, D8, D10: the in-memory copy must not outlive the rolling update ---

// A first-seen left behind after the update finished pre-expires the budget of the
// next one, which is ADR 0010 D10 reproduced in memory. clearRollingUpdateState is the one
// place every completion path funnels through.
func TestClearRollingUpdateState_ForgetsInMemoryWaitBounds(t *testing.T) {
	const name = "bound-clear"
	r, _, v, _ := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateVerifyingTopology,
		annotationPromotedPod:        name + "-1",
	}, nil)

	ctx := context.Background()
	r.ensureTopologyRestoreTimestamp(ctx, v)
	r.ensureFinalizationTimestamp(ctx, v)
	require.True(t, waitBoundArmed(r, name, boundTopologyRestore))
	require.True(t, waitBoundArmed(r, name, boundFinalization))

	require.NoError(t, r.clearRollingUpdateState(ctx, v))

	assert.False(t, waitBoundArmed(r, name, boundTopologyRestore),
		"a spent bound must not be inherited by the next rolling update")
	assert.False(t, waitBoundArmed(r, name, boundFinalization))
}

// The two Reconcile exits that never come back to the CR — it is gone, or it is
// being deleted — drop the tracker entries of the nudges and of the wait bounds.
func TestForgetNudges_AlsoDropsWaitBounds(t *testing.T) {
	const name = "bound-forget"
	r, _, v, _ := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-1",
	}, nil)

	r.ensureTopologyRestoreTimestamp(context.Background(), v)
	r.ensureFinalizationTimestamp(context.Background(), v)
	require.True(t, waitBoundArmed(r, name, boundTopologyRestore))

	r.forgetNudges(testNamespace, name)

	assert.False(t, waitBoundArmed(r, name, boundTopologyRestore),
		"a deleted CR must not leave wait bounds behind")
	assert.False(t, waitBoundArmed(r, name, boundFinalization))
}

// The nudge grace period keys by StatefulSet name, and for the data StatefulSet
// that name is the CR name. The wait bounds share the tracker, so their keys must
// be provably disjoint from it.
func TestWaitBoundKey_CannotCollideWithNudgeKey(t *testing.T) {
	v := newTestValkey("test", "default")
	stsKey := types.NamespacedName{Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: v.Namespace}
	require.Equal(t, v.Name, stsKey.Name, "the data StatefulSet is named after the CR")

	for _, bound := range []string{boundTopologyRestore, boundFinalization} {
		assert.NotEqual(t, stsKey, waitBoundKey(v.Namespace, v.Name, bound))
	}
	assert.NotEqual(t,
		waitBoundKey(v.Namespace, v.Name, boundTopologyRestore),
		waitBoundKey(v.Namespace, v.Name, boundFinalization))
}

// --- ADR 0007 D4: the new-pod guard has to see a config-only change ---

// The guard keeps REPLICAOF away from the old master pod that a stale cache still
// shows. Comparing the image alone made it void for every rolling update that does
// not change the image: the old pod passes, gets REPLICAOF, and the new pod comes
// up as an independent master with no data.
func TestHandlePostManualFailover_WaitsWhenOnlyTheConfigHashChanged(t *testing.T) {
	cases := []struct {
		testName   string
		crName     string
		staleAnnot string
	}{
		{testName: "config hash", crName: "guard-config", staleAnnot: builder.AnnotationConfigHash},
		{testName: "pod spec hash", crName: "guard-spec", staleAnnot: builder.AnnotationPodSpecHash},
	}

	for _, tc := range cases {
		t.Run(tc.testName, func(t *testing.T) {
			crName := tc.crName
			v := newTestValkey(crName, "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
			})
			v.Annotations = map[string]string{
				annotationRollingUpdateState: stateManualFailover,
				annotationPromotedPod:        crName + "-1",
			}
			sts := stsForValkey(v)

			// pod-0 is the old master the operator just deleted: same image as the
			// template (this update changes only the config), stale hash, still ready
			// because the cache has not caught up with the deletion yet.
			pod0 := podFromStsTemplate(v, sts, 0)
			pod0.Annotations[tc.staleAnnot] = "stale-from-the-previous-template"
			pod1 := podFromStsTemplate(v, sts, 1)
			pod2 := podFromStsTemplate(v, sts, 2)

			r, c := newTestReconciler(v, sts, pod0, pod1, pod2)

			var contacted []string
			addr := fakeValkeyServer(t)
			r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
				contacted = append(contacted, target)
				return valkeyclient.New(addr)
			}

			result := r.handlePostManualFailover(context.Background(), crGet(t, c, crName), sts)

			require.Nil(t, result.Error)
			assert.True(t, result.NeedsRequeue)
			assert.Empty(t, contacted,
				"the pod still carrying the old template is the pod that is about to die; "+
					"REPLICAOF must wait for its replacement")
			assert.Equal(t, stateManualFailover, crGet(t, c, crName).Annotations[annotationRollingUpdateState],
				"the state must not advance while the old master pod is still the one being seen")
		})
	}
}

// The image case the guard already covered must keep working, and the up-to-date
// pod must still be accepted — otherwise the guard would deadlock the phase.
func TestHandlePostManualFailover_GuardVerdicts(t *testing.T) {
	const name = "guard-image"

	t.Run("old image still waits", func(t *testing.T) {
		v := newTestValkey(name, "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
		v.Annotations = map[string]string{
			annotationRollingUpdateState: stateManualFailover,
			annotationPromotedPod:        name + "-1",
		}
		sts := stsForValkey(v)
		pod0 := podFromStsTemplate(v, sts, 0)
		for i := range pod0.Spec.Containers {
			if pod0.Spec.Containers[i].Name == builder.ValkeyContainerName {
				pod0.Spec.Containers[i].Image = "valkey/valkey:7.2"
			}
		}
		r, c := newTestReconciler(v, sts, pod0, podFromStsTemplate(v, sts, 1), podFromStsTemplate(v, sts, 2))

		var contacted []string
		addr := fakeValkeyServer(t)
		r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
			contacted = append(contacted, target)
			return valkeyclient.New(addr)
		}

		result := r.handlePostManualFailover(context.Background(), crGet(t, c, name), sts)
		require.Nil(t, result.Error)
		assert.True(t, result.NeedsRequeue)
		assert.Empty(t, contacted)
	})

	t.Run("matching pod is accepted", func(t *testing.T) {
		const okName = "guard-ok"
		r, c, v, sts := multiReplicaFixture(t, okName, map[string]string{
			annotationRollingUpdateState: stateManualFailover,
			annotationPromotedPod:        okName + "-1",
		}, nil)

		addr := fakeValkeyServer(t)
		r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
			return valkeyclient.New(addr)
		}

		result := r.handlePostManualFailover(context.Background(), v, sts)
		require.Nil(t, result.Error)
		assert.Equal(t, stateRestoringTopology, crGet(t, c, okName).Annotations[annotationRollingUpdateState],
			"a pod that matches the template is the new pod and must be sent REPLICAOF")
	})
}

// A pod that is being terminated is still rejected, on the same path.
func TestHandlePostManualFailover_WaitsForTerminatingPod(t *testing.T) {
	const name = "guard-term"
	v := newTestValkey(name, "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateManualFailover,
		annotationPromotedPod:        name + "-1",
	}
	sts := stsForValkey(v)
	pod0 := podFromStsTemplate(v, sts, 0)
	pod0.Finalizers = []string{"vko.gtrfc.com/test-hold"}

	r, c := newTestReconciler(v, sts, pod0, podFromStsTemplate(v, sts, 1), podFromStsTemplate(v, sts, 2))
	require.NoError(t, c.Delete(context.Background(), pod0))

	var contacted []string
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
		contacted = append(contacted, target)
		return valkeyclient.New(addr)
	}

	result := r.handlePostManualFailover(context.Background(), crGet(t, c, name), sts)
	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.Empty(t, contacted)

	// Clean up the hold so the fake client can finish the deletion.
	held := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: pod0.Name, Namespace: "default"}, held))
	held.Finalizers = nil
	require.NoError(t, c.Update(context.Background(), held))
}

// --- ADR 0010 D10 through the dispatch: completion must clear the state on every target ---

// handleStandaloneRollingUpdate reports Completed without clearing anything, and it
// is reachable with rolling-update state on the CR: scaling a multi-replica cluster
// down to one pod mid-restoration flips IsMultiReplicaWithoutSentinel, so the very
// next pass is dispatched to the standalone handler. The state annotations and the
// in-memory wait bounds then stayed behind permanently, and the spent bounds
// pre-expired the budget of the next rolling update.
func TestCheckAndHandleRollingUpdate_StandaloneDispatchClearsState(t *testing.T) {
	const name = "shrunk"
	v := newTestValkey(name, testNamespace, func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-1",
	}
	sts := stsForValkey(v)
	r, c := newTestReconciler(v, sts, podFromStsTemplate(v, sts, 0))

	ctx := context.Background()
	cr := crGet(t, c, name)
	require.False(t, cr.IsMultiReplicaWithoutSentinel(),
		"the shrunk CR must be dispatched to the standalone handler")

	r.ensureTopologyRestoreTimestamp(ctx, cr)
	r.ensureFinalizationTimestamp(ctx, cr)
	require.True(t, waitBoundArmed(r, name, boundTopologyRestore))
	require.True(t, waitBoundArmed(r, name, boundFinalization))

	result := r.checkAndHandleRollingUpdate(ctx, cr)

	require.Nil(t, result.Error)
	require.True(t, result.Completed)

	final := crGet(t, c, name)
	assert.Empty(t, final.Annotations[annotationRollingUpdateState],
		"a completed rolling update must not leave its state on the CR")
	assert.Empty(t, final.Annotations[annotationPromotedPod])
	assert.Empty(t, final.Annotations[annotationTopologyRestoreStarted])
	assert.Empty(t, final.Annotations[annotationFinalizationTimestamp])

	assert.False(t, waitBoundArmed(r, name, boundTopologyRestore),
		"a spent bound must not be inherited by the next rolling update")
	assert.False(t, waitBoundArmed(r, name, boundFinalization))
}

// --- ADR 0010 D10: entering Phase 2 must restart its budget ---

// The mirror of ADR 0010 D10 one state later. clearRollingUpdateState deletes
// finalization-started, but a rolling update that died in flight never reaches it and
// clearStaleRollingUpdateState only clears on the "nothing replaced yet" branch. Phase
// 2 therefore started against an hours-old timestamp, declared itself stalled on its
// very first pass and completed the rolling update WITHOUT consolidating rogue
// masters -- on the abandoned path the one job it has, and the last pass that can do
// it, because once the state annotation is gone nothing calls detectAndResolveSplitBrain
// again.
func TestAbandonTopologyRestoration_ReArmsPhase2BudgetOnEntry(t *testing.T) {
	const name = "phase2-rearm"
	promotedHost := fmt.Sprintf("%s-1.%s-headless.default.svc.cluster.local", name, name)

	r, c, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState:     stateRestoringTopology,
		annotationPromotedPod:            name + "-1",
		annotationTopologyRestoreStarted: time.Now().Add(-6 * time.Minute).UTC().Format(time.RFC3339),
		annotationFinalizationTimestamp:  time.Now().Add(-4 * time.Hour).UTC().Format(time.RFC3339),
		builder.AnnotationKnownMaster:    promotedHost,
	}, nil)
	r.InstanceChecker = pod0Unreachable(name)

	// Phase 1 gives up: pod-0 never synced back within the sync timeout.
	result := r.handleTopologyRestoration(context.Background(), v, sts)
	require.Nil(t, result.Error)
	require.True(t, result.NeedsRequeue)

	abandoned := crGet(t, c, name)
	require.Equal(t, stateVerifyingTopology, abandoned.Annotations[annotationRollingUpdateState])
	started, err := time.Parse(time.RFC3339, abandoned.Annotations[annotationFinalizationTimestamp])
	require.NoError(t, err, "Phase 2 must be timestamped when it is entered")
	assert.WithinDuration(t, time.Now(), started, time.Minute,
		"the budget of a rolling update that died earlier must not be inherited")
	assert.True(t, waitBoundArmed(r, name, boundFinalization),
		"the in-memory copy of the bound is re-armed at the same point")

	// The consequence, and the whole reason Phase 2 exists on this path: with a
	// rogue master present, the first pass must consolidate and requeue instead of
	// completing the rolling update unverified.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: common.RoleMaster}, nil
		},
	}
	result = r.verifyTopologyRestored(context.Background(), crGet(t, c, name), sts)

	require.Nil(t, result.Error)
	assert.False(t, result.Completed,
		"a Phase 2 that just started must not report itself stalled on its first pass")
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, stateVerifyingTopology, crGet(t, c, name).Annotations[annotationRollingUpdateState],
		"the state has to stay until the masters are consolidated or the budget is spent")
}

// The second entry into Phase 2, the normal one: pod-0 was promoted back. It inherits
// the same stale timestamp from the same causes, so it arms the same budget.
func TestPromotePod0AndRedirect_ArmsPhase2BudgetOnEntry(t *testing.T) {
	const name = "phase2-promote"
	r, c, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState:    stateRestoringTopology,
		annotationPromotedPod:           name + "-1",
		annotationFinalizationTimestamp: time.Now().Add(-4 * time.Hour).UTC().Format(time.RFC3339),
	}, nil)

	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	result := r.promotePod0AndRedirect(context.Background(), v, sts, name+"-0")
	require.Nil(t, result.Error)
	require.True(t, result.NeedsRequeue)

	promoted := crGet(t, c, name)
	require.Equal(t, stateVerifyingTopology, promoted.Annotations[annotationRollingUpdateState])
	started, err := time.Parse(time.RFC3339, promoted.Annotations[annotationFinalizationTimestamp])
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), started, time.Minute,
		"Phase 2 gets its own budget on this entry too")
	assert.True(t, waitBoundArmed(r, name, boundFinalization))
}

// --- ADR 0010 D6: the manual-failover wait needs a bound of its own ---

// manualFailoverFixture builds the state the operator is in right after
// handleManualFailover: a replica promoted, the state persisted, pod-0 deleted and
// not back. started seeds the bound; an empty value leaves it unarmed.
func manualFailoverFixture(t *testing.T, name, started string) (*ValkeyReconciler, client.Client,
	*vkov1.Valkey, *appsv1.StatefulSet) {
	t.Helper()

	annotations := map[string]string{
		annotationRollingUpdateState:  stateManualFailover,
		annotationPromotedPod:         name + "-1",
		builder.AnnotationKnownMaster: fmt.Sprintf("%s-1.%s-headless.default.svc.cluster.local", name, name),
	}
	if started != "" {
		annotations[annotationManualFailoverStarted] = started
	}

	r, c, _, sts := multiReplicaFixture(t, name, annotations, nil)

	// pod-0 was deleted by the failover and the StatefulSet never got it back --
	// a rejected CREATE, a PVC that cannot bind, an ImagePullBackOff.
	pod0 := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name + "-0", Namespace: testNamespace}, pod0))
	require.NoError(t, c.Delete(context.Background(), pod0))

	return r, c, crGet(t, c, name), sts
}

// [REGRESSION] Every wait branch of handlePostManualFailover returned a bare requeue,
// so a pod-0 that never comes back parks the state machine in manual-failover forever:
// the cluster serves from the temporary master with nothing declaring it the end
// state, TopologyRestored is never written, the phase freezes at "Rolling Update N/M",
// and because Reconcile returns on NeedsRequeue the ADR 0011 D1 steady-state check and
// updateStatus never run for the whole duration.
//
// The escape has to be Phase 2 rather than a cleared state: Phase 2 is the last pass
// that consolidates the masters a half-finished failover leaves behind.
func TestHandlePostManualFailover_AbandonsIntoPhase2WhenPodZeroNeverReturns(t *testing.T) {
	const name = "mf-bound"
	// syncTimeout defaults to 5m.
	r, c, v, sts := manualFailoverFixture(t, name, time.Now().Add(-6*time.Minute).UTC().Format(time.RFC3339))
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	result := r.handlePostManualFailover(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue, "Phase 2 still has to run")

	final := crGet(t, c, name)
	assert.Equal(t, stateVerifyingTopology, final.Annotations[annotationRollingUpdateState],
		"a spent budget hands the rolling update to Phase 2, it does not clear the state")
	assert.Equal(t, fmt.Sprintf("%s-1.%s-headless.default.svc.cluster.local", name, name),
		final.Annotations[builder.AnnotationKnownMaster],
		"the promoted replica holds the writes and stays master")

	cond := apimeta.FindStatusCondition(final.Status.Conditions, vkov1.ConditionTypeTopologyRestored)
	require.NotNil(t, cond, "a non-canonical topology has to leave a durable trace")
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "RestoreTimeout", cond.Reason)

	abandoned := rec.withReason("TopologyRestoreAbandoned")
	require.Len(t, abandoned, 1)
	assert.Contains(t, abandoned[0].note, name+"-0", "the Event has to name the pod that never came back")

	// ADR 0010 D10 is what makes this handover worth anything: Phase 2 arrives with a budget
	// of its own instead of one an earlier update already spent.
	started, err := time.Parse(time.RFC3339, final.Annotations[annotationFinalizationTimestamp])
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), started, time.Minute)
}

// Inside the budget nothing changes except that the bound is now armed -- the wait is
// legitimate, pod-0 may still come back, and the state machine keeps waiting for it.
func TestHandlePostManualFailover_ArmsTheBoundAndKeepsWaiting(t *testing.T) {
	const name = "mf-wait"
	r, c, v, sts := manualFailoverFixture(t, name, "")

	result := r.handlePostManualFailover(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)

	current := crGet(t, c, name)
	assert.Equal(t, stateManualFailover, current.Annotations[annotationRollingUpdateState],
		"a wait that just started must not be abandoned")
	started, err := time.Parse(time.RFC3339, current.Annotations[annotationManualFailoverStarted])
	require.NoError(t, err, "the wait has to be timestamped on the first pass that reaches it")
	assert.WithinDuration(t, time.Now(), started, time.Minute)
	assert.True(t, waitBoundArmed(r, name, boundManualFailover),
		"the in-memory copy carries the deadline when the annotation write cannot land")
	assert.Nil(t, apimeta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeTopologyRestored))
}

// The bound is armed where the state is written, not only where it is waited on, for
// the ADR 0010 D10 reason: a manual-failover timestamp left behind by a rolling update that
// died would otherwise be inherited by the next one and expire on its first pass.
func TestPersistManualFailoverState_ArmsTheBoundOnEntry(t *testing.T) {
	const name = "mf-arm"
	promotedHost := fmt.Sprintf("%s-1.%s-headless.default.svc.cluster.local", name, name)
	r, c, v, _ := multiReplicaFixture(t, name, map[string]string{
		annotationManualFailoverStarted: time.Now().Add(-4 * time.Hour).UTC().Format(time.RFC3339),
	}, nil)

	require.NoError(t, r.persistManualFailoverState(context.Background(), v, name+"-1", promotedHost))

	stored := crGet(t, c, name)
	require.Equal(t, stateManualFailover, stored.Annotations[annotationRollingUpdateState])
	started, err := time.Parse(time.RFC3339, stored.Annotations[annotationManualFailoverStarted])
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), started, time.Minute,
		"a stale timestamp from an earlier update must not pre-expire this one")
	assert.True(t, waitBoundArmed(r, name, boundManualFailover))
	assert.False(t, r.isManualFailoverStalled(stored))
}

// The spent bound must not outlive the rolling update, in either copy: the annotation
// or the tracker. A leftover is ADR 0010 D10 for the next failover.
func TestClearRollingUpdateState_ForgetsTheManualFailoverBound(t *testing.T) {
	const name = "mf-clear"
	r, c, v, _ := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateManualFailover,
		annotationPromotedPod:        name + "-1",
	}, nil)

	ctx := context.Background()
	r.ensureManualFailoverTimestamp(ctx, v)
	require.True(t, waitBoundArmed(r, name, boundManualFailover))
	require.NotEmpty(t, crGet(t, c, name).Annotations[annotationManualFailoverStarted])

	require.NoError(t, r.clearRollingUpdateState(ctx, v))

	assert.Empty(t, crGet(t, c, name).Annotations[annotationManualFailoverStarted])
	assert.False(t, waitBoundArmed(r, name, boundManualFailover))
}

// --- ADR 0010 D14: the two bounds that used to arm with a discarded write error ---

// rejectAnnotationArming refuses every CR update that carries the given
// annotation — a fail-closed admission webhook on the CR, or any other permanent
// rejection, seen from the arming write's perspective.
func rejectAnnotationArming(annotation string, attempts *int) interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			cr, isCR := obj.(*vkov1.Valkey)
			if isCR && cr.Annotations[annotation] != "" {
				*attempts++
				return apierrors.NewInternalError(fmt.Errorf("admission webhook denied the annotation"))
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
}

// A CR whose writes keep failing used to leave the sentinel-awareness bound
// unarmed forever: isSentinelAwarenessStalled read an annotation that never
// landed, answered false on every pass, and both of its call sites requeued the
// Sentinel rolling update indefinitely. The in-memory copy must carry the
// deadline instead.
func TestSentinelAwarenessBound_HoldsWhenArmingWriteFails(t *testing.T) {
	const name = "sentinel-arm-blocked"
	attempts := 0
	funcs := rejectAnnotationArming(annotationSentinelAwarenessStarted, &attempts)
	r, c, v, _ := multiReplicaFixture(t, name, nil, &funcs)

	r.ensureSentinelAwarenessTimestamp(context.Background(), v)

	require.Positive(t, attempts, "the arming write must have been attempted")
	require.Empty(t, crGet(t, c, name).Annotations[annotationSentinelAwarenessStarted],
		"the fixture only works while the annotation truly cannot be persisted")
	assert.Empty(t, v.Annotations[annotationSentinelAwarenessStarted],
		"the in-memory object must reflect what was persisted")
	require.True(t, waitBoundArmed(r, name, boundSentinelAwareness),
		"a rejected annotation write must still leave the bound armed in memory")

	assert.False(t, r.isSentinelAwarenessStalled(crGet(t, c, name)),
		"a freshly armed bound must not already report stalled")
	rewindWaitBound(t, r, name, boundSentinelAwareness, sentinelAwarenessTimeout+time.Second)
	assert.True(t, r.isSentinelAwarenessStalled(crGet(t, c, name)),
		"the in-memory deadline must end the wait when the annotation can never be written")
}

// Both re-baselining sites of a SENTINEL RESET must drop the in-memory copy along
// with the annotation: the tracker is first-seen-wins, so a leftover entry would
// pre-expire the next attempt's budget and push the operator into a failover
// Sentinel is not ready for.
func TestSentinelAwarenessBound_ResetRebaselines(t *testing.T) {
	const name = "sentinel-rebaseline"
	r, _, v, _ := multiReplicaFixture(t, name, nil, nil)
	ctx := context.Background()

	r.ensureSentinelAwarenessTimestamp(ctx, v)
	require.True(t, waitBoundArmed(r, name, boundSentinelAwareness))

	require.NoError(t, r.incrementReconnectResetCount(ctx, v, 1))
	assert.False(t, waitBoundArmed(r, name, boundSentinelAwareness),
		"incrementReconnectResetCount re-baselines after a reset and must drop the in-memory copy")

	r.ensureSentinelAwarenessTimestamp(ctx, v)
	require.True(t, waitBoundArmed(r, name, boundSentinelAwareness))

	r.clearSentinelAwarenessTimestamp(v)
	assert.False(t, waitBoundArmed(r, name, boundSentinelAwareness),
		"clearSentinelAwarenessTimestamp must drop the in-memory copy for the same reason")
}

// The sync-wait bound had the same defect on the non-Sentinel path: with CR
// writes failing persistently, verifyReplacedReplicasSynced requeued forever and
// never reached pauseRollingUpdate.
func TestSyncWaitBound_HoldsWhenArmingWriteFails(t *testing.T) {
	const name = "syncwait-arm-blocked"
	attempts := 0
	funcs := rejectAnnotationArming(annotationSyncWaitStarted, &attempts)
	r, c, v, _ := multiReplicaFixture(t, name, nil, &funcs)

	r.ensureSyncWaitTimestamp(context.Background(), v)

	require.Positive(t, attempts, "the arming write must have been attempted")
	require.Empty(t, crGet(t, c, name).Annotations[annotationSyncWaitStarted],
		"the fixture only works while the annotation truly cannot be persisted")
	require.True(t, waitBoundArmed(r, name, boundSyncWait),
		"a rejected annotation write must still leave the bound armed in memory")

	assert.False(t, r.isSyncWaitTimedOut(crGet(t, c, name)))
	rewindWaitBound(t, r, name, boundSyncWait, v.GetSyncTimeout()+time.Minute)
	assert.True(t, r.isSyncWaitTimedOut(crGet(t, c, name)),
		"the in-memory deadline must end the wait when the annotation can never be written")
}

// clearSyncWaitTimestamp runs mid-update, once every replaced replica is synced.
// It must drop the in-memory copy too: the next sync wait of the same rolling
// update would otherwise start against a spent budget and pause the update for a
// timeout that never elapsed.
func TestClearSyncWaitTimestamp_ForgetsTheBound(t *testing.T) {
	const name = "syncwait-clear"
	r, c, v, _ := multiReplicaFixture(t, name, nil, nil)
	ctx := context.Background()

	r.ensureSyncWaitTimestamp(ctx, v)
	require.True(t, waitBoundArmed(r, name, boundSyncWait))
	require.NotEmpty(t, crGet(t, c, name).Annotations[annotationSyncWaitStarted])

	r.clearSyncWaitTimestamp(ctx, v)

	assert.False(t, waitBoundArmed(r, name, boundSyncWait))
	assert.Empty(t, crGet(t, c, name).Annotations[annotationSyncWaitStarted])
}
