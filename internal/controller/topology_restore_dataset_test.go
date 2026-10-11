package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The tests in this file pin the dataset veto in front of the topology restoration
// (docs/adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md, D9).
// promotePod0AndRedirect points every other pod at pod-0, so an empty pod-0 promoted
// back over a pod that holds the data takes every key with it. The fixture is the
// three-pod non-Sentinel cluster parked in restoring-topology; the data pods answer
// DBSIZE per ordinal through keysPerOrdinal.

// pod0Synced answers every replication question with a synced replica, so Phase 1's
// sync wait passes and the veto is the only thing left to stop the promotion.
func pod0Synced() *mockInstanceChecker {
	return &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}
}

// hostOf is the address the operator writes into a REPLICAOF and into known-master.
func hostOf(name string, ordinal int) string {
	return fmt.Sprintf("%s-%d.%s-headless.default.svc.cluster.local", name, ordinal, name)
}

func TestPromotePod0AndRedirect_SendsNothingThatWouldDiscardTheOnlyDataset(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cr       string
		keys     map[int]int
		gone     int // ordinal of a pod that no longer exists, -1 for none
		promoted bool
	}{
		{name: "pod-0 holds the dataset", cr: "veto-full",
			keys: map[int]int{0: 500, 1: 500, 2: 500}, gone: -1, promoted: true},
		{name: "every pod empty", cr: "veto-empty",
			keys: map[int]int{0: 0, 1: 0, 2: 0}, gone: -1, promoted: true},
		{name: "pod-0 empty, another pod holds the dataset", cr: "veto-holder",
			keys: map[int]int{0: 0, 1: 0, 2: 500}, gone: -1, promoted: false},
		{name: "pod-0 count unreadable", cr: "veto-unread0",
			keys: map[int]int{0: unreadableKeys, 1: 500, 2: 500}, gone: -1, promoted: false},
		{name: "pod-0 empty, a count unreadable", cr: "veto-unread2",
			keys: map[int]int{0: 0, 1: 0, 2: unreadableKeys}, gone: -1, promoted: false},
		// A pod that is not there holds nothing a REPLICAOF could discard, which is the
		// "every other existing pod" of ADR 0037 D4.
		{name: "pod-0 empty, the only holder no longer exists", cr: "veto-gone",
			keys: map[int]int{0: 0, 1: 0, 2: 500}, gone: 2, promoted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.cr
			r, c, v, sts := topologyRestoreFixture(t, name, map[string]string{
				annotationRollingUpdateState:  stateRestoringTopology,
				annotationPromotedPod:         name + "-1",
				builder.AnnotationKnownMaster: hostOf(name, 1),
			})
			if tc.gone >= 0 {
				require.NoError(t, c.Delete(context.Background(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("%s-%d", name, tc.gone), Namespace: "default"}}))
			}
			r.InstanceChecker = pod0Synced()
			router := newRESPRouter(t, keysPerOrdinal(v, tc.keys))
			router.attach(r)

			result := r.promotePod0AndRedirect(context.Background(), v, sts, name+"-0")

			require.Nil(t, result.Error)
			assert.True(t, result.NeedsRequeue)
			updated := crGet(t, c, name)

			if tc.promoted {
				assert.Contains(t, router.commandsTo(dataAddr(v, 0)), "REPLICAOF NO ONE")
				assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState])
				assert.Equal(t, hostOf(name, 0), updated.Annotations[builder.AnnotationKnownMaster])
				return
			}

			assert.Empty(t, router.targetsFor("REPLICAOF"),
				"neither the promotion nor a single redirect may be sent")
			counted := router.targetsFor("DBSIZE")
			require.NotEmpty(t, counted)
			assert.Equal(t, dataAddr(v, 0), counted[0], "pod-0 is counted first")
			assert.Equal(t, stateRestoringTopology, updated.Annotations[annotationRollingUpdateState],
				"the restoration keeps waiting")
			assert.NotEmpty(t, updated.Annotations[annotationTopologyRestoreStarted],
				"the wait runs on the Phase 1 bound, or a refusal would wait forever")
			assert.Equal(t, hostOf(name, 1), updated.Annotations[builder.AnnotationKnownMaster],
				"nothing was promoted, so nothing may be recorded")
			assert.Nil(t, apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored),
				"no verdict while the wait is still legitimate")
		})
	}
}

// The shape CI measured: the promoted replica was killed mid-roll and came back empty,
// the drain promoted the pod that holds the data, and pod-0 -- a fully synced replica
// of the empty pod -- passed the sync wait. Before the veto the same pass promoted it
// and redirected the holder at it, and every key was lost. With the veto pod-0 waits
// until the dataset has reached it through the chain, and only then takes the role.
func TestHandleTopologyRestoration_AnEmptyUpstreamDoesNotHandPod0TheMaster(t *testing.T) {
	const name = "topo-empty-upstream"
	r, c, v, sts := topologyRestoreFixture(t, name, map[string]string{
		annotationRollingUpdateState:  stateRestoringTopology,
		annotationPromotedPod:         name + "-1",
		builder.AnnotationKnownMaster: hostOf(name, 2),
	})
	r.InstanceChecker = pod0Synced()

	first := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 0, 1: 0, 2: 3}))
	first.attach(r)

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.Empty(t, first.targetsFor("REPLICAOF"), "the holder must not be pointed at an empty pod-0")
	updated := crGet(t, c, name)
	assert.Equal(t, stateRestoringTopology, updated.Annotations[annotationRollingUpdateState])
	assert.Equal(t, hostOf(name, 2), updated.Annotations[builder.AnnotationKnownMaster])

	// The next pass finds the dataset on pod-0, and the restoration goes ahead.
	second := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 3, 1: 3, 2: 3}))
	second.attach(r)

	result = r.handleTopologyRestoration(context.Background(), updated, sts)

	require.Nil(t, result.Error)
	assert.Contains(t, second.commandsTo(dataAddr(v, 0)), "REPLICAOF NO ONE")
	for _, ordinal := range []int{1, 2} {
		assert.Contains(t, second.commandsTo(dataAddr(v, ordinal)),
			fmt.Sprintf("REPLICAOF %s 6379", hostOf(name, 0)))
	}
	updated = crGet(t, c, name)
	assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState])
	assert.Equal(t, hostOf(name, 0), updated.Annotations[builder.AnnotationKnownMaster])
}

// The self-loop branch skips the sync wait, so the veto is the only check between it
// and a promotion; it must apply there too.
func TestHandleTopologyRestoration_TheSelfLoopIsVetoedToo(t *testing.T) {
	const name = "topo-selfloop-veto"
	r, c, v, sts := topologyRestoreFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-0",
	})
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "down"}, nil
		},
	}
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 0, 1: 500, 2: 500}))
	router.attach(r)

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.Empty(t, router.targetsFor("REPLICAOF"))
	assert.Equal(t, stateRestoringTopology, crGet(t, c, name).Annotations[annotationRollingUpdateState])
}

// A refusal is a Phase 1 wait like any other: past spec.rollingUpdate.syncTimeout the
// restoration is abandoned, the master stays where it is, and the verdict names why.
func TestHandleTopologyRestoration_ARefusalPastTheBoundAbandons(t *testing.T) {
	const name = "topo-veto-abandon"
	stale := time.Now().Add(-(6 * time.Minute)).UTC().Format(time.RFC3339) // default syncTimeout is 5m
	r, c, v, sts := topologyRestoreFixture(t, name, map[string]string{
		annotationRollingUpdateState:     stateRestoringTopology,
		annotationPromotedPod:            name + "-1",
		annotationTopologyRestoreStarted: stale,
		builder.AnnotationKnownMaster:    hostOf(name, 2),
	})
	r.InstanceChecker = pod0Synced()
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 0, 1: 0, 2: 500}))
	router.attach(r)

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	require.Nil(t, result.Error)
	assert.Empty(t, router.targetsFor("REPLICAOF"))
	updated := crGet(t, c, name)
	assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState],
		"the refusal escapes into Phase 2 like every other Phase 1 stall")
	assert.Equal(t, hostOf(name, 2), updated.Annotations[builder.AnnotationKnownMaster],
		"the pod holding the data stays the recorded master")

	cond := apimeta.FindStatusCondition(updated.Status.Conditions, vkov1.ConditionTypeTopologyRestored)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "RestoreTimeout", cond.Reason)
	assert.Contains(t, cond.Message, name+"-0 holds no keys while "+name+"-2 holds 500")
}
