package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/health"
)

// Sentinel never forgets a peer it has seen, so a replaced sentinel pod that
// announced a new identity stays in the table of every survivor. The majority a
// failover leader needs is computed over that whole table, which is why the count
// is reported rather than left in the logs.

func valkeyWithSentinel() *vkov1.Valkey {
	v := &vkov1.Valkey{}
	v.Name = "test"
	v.Namespace = "default"
	v.Generation = 7
	v.Spec.Replicas = 3
	v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	return v
}

func TestRecordSentinelPeerDrift_ReportsTheInflatedTables(t *testing.T) {
	v := valkeyWithSentinel()

	(&ValkeyReconciler{}).recordSentinelPeerDrift(context.Background(), v, &health.ClusterState{
		SentinelPeersExpected: 2,
		SentinelPeers: map[string]int{
			"test-sentinel-0": 4,
			"test-sentinel-1": 3,
			"test-sentinel-2": 2,
		},
	})

	condition := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelPeersStale)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, "StaleSentinelEntries", condition.Reason)
	assert.Equal(t, int64(7), condition.ObservedGeneration)
	assert.Contains(t, condition.Message, "Expected 2 other Sentinels")
	assert.Contains(t, condition.Message, "test-sentinel-0 knows 4, test-sentinel-1 knows 3")
	assert.NotContains(t, condition.Message, "test-sentinel-2",
		"a sentinel with the expected count is not an offender")
}

// The message has to be byte-stable across passes. Built from a map without
// sorting it would reorder itself, and persistStatus compares conditions -- so a
// healthy cluster would write its status on every single reconcile.
func TestRecordSentinelPeerDrift_MessageIsStableAcrossPasses(t *testing.T) {
	state := &health.ClusterState{
		SentinelPeersExpected: 2,
		SentinelPeers: map[string]int{
			"test-sentinel-0": 5,
			"test-sentinel-1": 4,
			"test-sentinel-2": 3,
		},
	}

	first := valkeyWithSentinel()
	(&ValkeyReconciler{}).recordSentinelPeerDrift(context.Background(), first, state)
	want := meta.FindStatusCondition(first.Status.Conditions, vkov1.ConditionTypeSentinelPeersStale).Message

	for i := 0; i < 20; i++ {
		v := valkeyWithSentinel()
		(&ValkeyReconciler{}).recordSentinelPeerDrift(context.Background(), v, state)
		got := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelPeersStale).Message
		require.Equal(t, want, got, "the offender list must not depend on map iteration order")
	}
}

// Clearing is explicit rather than by removal: the condition is what the
// SENTINEL RESET of an existing cluster is verified against, and a series that
// disappears is harder to tell from one that was never evaluated.
func TestRecordSentinelPeerDrift_ClearsWhenTheTablesAgree(t *testing.T) {
	v := valkeyWithSentinel()
	meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:    vkov1.ConditionTypeSentinelPeersStale,
		Status:  metav1.ConditionTrue,
		Reason:  "StaleSentinelEntries",
		Message: "from an earlier pass",
	})

	(&ValkeyReconciler{}).recordSentinelPeerDrift(context.Background(), v, &health.ClusterState{
		SentinelPeersExpected: 2,
		SentinelPeers: map[string]int{
			"test-sentinel-0": 2,
			"test-sentinel-1": 2,
			"test-sentinel-2": 2,
		},
	})

	condition := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelPeersStale)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, "SentinelPeersConsistent", condition.Reason)
}

// An unreachable sentinel tier measures nothing. Writing False on that would
// clear the one signal an operator is supposed to act on, using three failed
// connections as the evidence that the tables are fine.
func TestRecordSentinelPeerDrift_NoAnswerLeavesTheConditionAlone(t *testing.T) {
	v := valkeyWithSentinel()
	meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:    vkov1.ConditionTypeSentinelPeersStale,
		Status:  metav1.ConditionTrue,
		Reason:  "StaleSentinelEntries",
		Message: "measured while the sentinels were reachable",
	})

	(&ValkeyReconciler{}).recordSentinelPeerDrift(context.Background(), v, &health.ClusterState{SentinelPeersExpected: 2})

	condition := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelPeersStale)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, "measured while the sentinels were reachable", condition.Message)
}

// A partially reachable tier still reports what it saw. The sentinels that
// answered carry real counts, and expecting all of them to answer before saying
// anything would silence the check exactly when a sentinel is missing -- which is
// when the remaining margin matters most.
func TestRecordSentinelPeerDrift_ReportsOnThePodsThatAnswered(t *testing.T) {
	v := valkeyWithSentinel()

	(&ValkeyReconciler{}).recordSentinelPeerDrift(context.Background(), v, &health.ClusterState{
		SentinelPeersExpected: 2,
		SentinelPeers:         map[string]int{"test-sentinel-0": 4},
	})

	condition := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeSentinelPeersStale)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Contains(t, condition.Message, "test-sentinel-0 knows 4")
}

// Clearing the entries is a manual SENTINEL RESET, which changes no Kubernetes
// object at all: no pod restarts, no owned-object event, and the CR watch is
// generation-gated. Without a recheck the condition would keep asserting drift
// until the 10 h cache resync, which is the one window an operator needs it in.
func TestRecordSentinelPeerDrift_DriftAsksToBeLookedAtAgain(t *testing.T) {
	ctx, state := passCtx()

	(&ValkeyReconciler{}).recordSentinelPeerDrift(ctx, valkeyWithSentinel(), &health.ClusterState{
		SentinelPeersExpected: 2,
		SentinelPeers:         map[string]int{"test-sentinel-0": 4},
	})

	assert.Equal(t, sentinelPeerDriftRecheckInterval, state.interval())
}

// The converse: a healthy cluster must not poll. Peer tables change when a
// Sentinel pod is replaced, and that already produces StatefulSet events the
// controller watches -- paying a recheck on every sentinel cluster in the fleet
// would buy nothing.
func TestRecordSentinelPeerDrift_ConsistentTablesDoNotPoll(t *testing.T) {
	ctx, state := passCtx()

	(&ValkeyReconciler{}).recordSentinelPeerDrift(ctx, valkeyWithSentinel(), &health.ClusterState{
		SentinelPeersExpected: 2,
		SentinelPeers: map[string]int{
			"test-sentinel-0": 2,
			"test-sentinel-1": 2,
			"test-sentinel-2": 2,
		},
	})

	assert.Zero(t, state.interval())
}
