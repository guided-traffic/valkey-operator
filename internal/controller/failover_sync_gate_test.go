package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The gates in this file guard the one irreversible step of the rolling update:
// the promotion of a replica, immediately followed by the delete of the outgoing
// master. Whatever passes them is treated as holding the dataset, and with the old
// master gone there is no second copy to fall back on.
//
// Each test names the state that must NOT pass. Reverting the gate to the
// master_sync_in_progress-only question it asked before turns the blocking ones
// green in the wrong direction: they stop blocking.

// replicaInfoLink is replicaInfo with the link state spelled out, which is the
// field these gates turn on.
func replicaInfoLink(linkStatus string, syncing bool) *valkeyclient.ReplicationInfo {
	return &valkeyclient.ReplicationInfo{
		Role:                 "slave",
		MasterHost:           "gate-0.gate-headless.default.svc.cluster.local",
		MasterPort:           "6379",
		MasterLinkStatus:     linkStatus,
		MasterSyncInProgress: syncing,
	}
}

// gateCluster returns a two-pod rolling update: pod-0 is the master that still
// needs the new image, pod-1 is the replaced replica about to be promoted.
func gateCluster(t *testing.T, name string, annotations map[string]string) (
	*ValkeyReconciler, client.Client, *vkov1.Valkey, []podState) {
	t.Helper()

	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 2
		if annotations != nil {
			v.Annotations = annotations
		}
	})
	r, c := newTestReconciler(v)

	pods := []podState{
		{name: name + "-0", exists: true, ready: true, needsUpdate: true, isMaster: true},
		{name: name + "-1", exists: true, ready: true, needsUpdate: false},
	}
	return r, c, v, pods
}

// A replica that has been told to replicate but whose link is still in
// CONNECT/CONNECTING reports master_sync_in_progress:0 with master_link_status:down
// -- it has received nothing. Promoting it and deleting the master is the data loss
// this gate exists to prevent.
func TestWaitForReplicasReady_BlocksAReplicaWhoseLinkIsDown(t *testing.T) {
	r, _, v, pods := gateCluster(t, "gate-down", nil)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return replicaInfoLink("down", false), nil
		},
	}

	result := r.waitForReplicasReady(context.Background(), v, pods, 0)

	require.NotNil(t, result, "a replica with no replication link must not pass the failover gate")
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.NotEmpty(t, v.Annotations[annotationSyncWaitStarted],
		"the wait must arm its bound, otherwise it can never end")
}

// A pod that answers role:master is not replicating from anywhere, so it cannot be
// known to hold the master dataset either.
func TestWaitForReplicasReady_BlocksAReplicaThatAnswersMaster(t *testing.T) {
	r, _, v, pods := gateCluster(t, "gate-master", nil)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "master"}, nil
		},
	}

	result := r.waitForReplicasReady(context.Background(), v, pods, 0)

	require.NotNil(t, result, "a pod that answers role:master must not pass as a synced replica")
	assert.True(t, result.NeedsRequeue)
}

// The positive control: an established link with no sync running is what the gate
// is meant to let through, and it must also disarm the bound it may have armed
// earlier in the same update.
func TestWaitForReplicasReady_PassesAnEstablishedReplicaAndDisarmsTheBound(t *testing.T) {
	r, _, v, pods := gateCluster(t, "gate-up", map[string]string{
		annotationSyncWaitStarted: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
	})
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return replicaInfoLink("up", false), nil
		},
	}

	assert.Nil(t, r.waitForReplicasReady(context.Background(), v, pods, 0))
	_, armed := v.Annotations[annotationSyncWaitStarted]
	assert.False(t, armed,
		"a leftover bound would pre-expire the next sync wait of the same rolling update")
}

// The bound has to end somewhere, and the direction matters: a paused rolling update
// is recoverable, a promotion onto a replica that never synced is not.
func TestWaitForReplicasReady_PausesTheUpdateOnceTheBoundExpired(t *testing.T) {
	r, c, v, pods := gateCluster(t, "gate-expired", map[string]string{
		annotationSyncWaitStarted: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
	})
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return replicaInfoLink("down", false), nil
		},
	}

	result := r.waitForReplicasReady(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.False(t, result.NeedsRequeue, "a paused update waits for a new spec change, not a requeue")
	assert.Nil(t, result.Error)

	condition := conditionOf(t, c, v, vkov1.ConditionTypeRollingUpdatePaused)
	require.NotNil(t, condition, "the pause must be visible on the CR, not only in the log")
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Contains(t, condition.Message, "replication not established",
		"the pause must name what did not complete")
}

// An unreachable replica is an unanswered question, not a negative answer, and it
// must be treated the same way: wait, bounded, and never fail over on it.
func TestWaitForReplicasReady_BlocksWhenTheReplicaCannotBeAsked(t *testing.T) {
	r, _, v, pods := gateCluster(t, "gate-unreachable", nil)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	result := r.waitForReplicasReady(context.Background(), v, pods, 0)

	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.NotEmpty(t, v.Annotations[annotationSyncWaitStarted],
		"an unreachable replica must arm the bound too, or the wait is unbounded")
}

// WAIT returning zero is the master saying no replica confirmed its offset. It is
// not a cascaded-chain partial result, and accepting it as one promotes a pod that
// nothing proves has the data.
func TestWaitForWriteSync_RefusesToFailOverWithoutASingleAcknowledgement(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "wws-zero", nil, nil)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) > 0 && strings.EqualFold(args[0], "WAIT") {
			return respInt(0)
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	result := r.waitForWriteSync(context.Background(), v, pods, 0)

	require.NotNil(t, result, "zero acknowledgements must not read as safe to fail over")
	assert.True(t, result.NeedsRequeue)
	assert.NotEmpty(t, v.Annotations[annotationSyncWaitStarted],
		"the retry must be bounded, otherwise a dead replica stalls the update forever")
}

// The promotion candidate holding nothing while the master holds data is the state
// this guard exists for: the promotion is followed by the delete of the master, so
// there is no second copy to fall back on.
func TestVerifyPromotionCandidateHoldsData_RefusesAnEmptyCandidate(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "dbz-empty", nil, nil)
	router := newRESPRouter(t, func(target string, args []string) string {
		if len(args) > 0 && strings.EqualFold(args[0], "DBSIZE") && target == dataAddr(v, 1) {
			return respInt(0)
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	result := r.verifyPromotionCandidateHoldsData(context.Background(), v, pods[0], pods[1])

	require.NotNil(t, result, "a candidate with no keys must not be promoted over a master that has some")
	assert.True(t, result.NeedsRequeue)
	assert.NotEmpty(t, v.Annotations[annotationSyncWaitStarted], "the refusal must be bounded")
}

// The positive control: both sides hold data, which is the normal case and must not
// be slowed down or blocked by the guard.
func TestVerifyPromotionCandidateHoldsData_PassesWhenBothHoldData(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "dbz-ok", nil, nil)
	router := newRESPRouter(t, func(_ string, args []string) string { return clusterAnswer(2, args) })
	router.attach(r)

	assert.Nil(t, r.verifyPromotionCandidateHoldsData(context.Background(), v, pods[0], pods[1]))
}

// An empty cluster is a legitimate state. Refusing there would stall the rolling
// update of every cluster that holds no data yet.
func TestVerifyPromotionCandidateHoldsData_PassesWhenTheMasterIsEmptyToo(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "dbz-both-empty", nil, nil)
	router := newRESPRouter(t, func(_ string, args []string) string {
		if len(args) > 0 && strings.EqualFold(args[0], "DBSIZE") {
			return respInt(0)
		}
		return clusterAnswer(2, args)
	})
	router.attach(r)

	assert.Nil(t, r.verifyPromotionCandidateHoldsData(context.Background(), v, pods[0], pods[1]),
		"an empty master has nothing to lose, so the promotion must proceed")
}

// An unreadable count is an unanswered question, and the failover must not treat it
// as a yes.
func TestVerifyPromotionCandidateHoldsData_WaitsWhenTheCountCannotBeRead(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "dbz-unreachable", nil, nil)
	// No router attached: every client dials an address nothing listens on.

	result := r.verifyPromotionCandidateHoldsData(context.Background(), v, pods[0], pods[1])

	require.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
}
