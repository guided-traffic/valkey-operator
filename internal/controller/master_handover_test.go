package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The tests in this file pin the handover gate in front of the delete of the outgoing
// master (docs/adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md,
// D4, D5, D6). The data pods answer DBSIZE per ordinal, so a test says which pod holds
// what; a negative count makes a pod's DBSIZE fail.

// unreadableKeys marks a pod whose key count cannot be read.
const unreadableKeys = -1

// keysPerOrdinal answers DBSIZE per data pod ordinal, SENTINEL MASTER with pod-1 --
// the new master in every fixture here -- and everything else with OK.
func keysPerOrdinal(v *vkov1.Valkey, keys map[int]int) func(string, []string) string {
	return func(target string, args []string) string {
		if len(args) == 0 {
			return respOK
		}
		switch strings.ToUpper(args[0]) {
		case "DBSIZE":
			for ordinal, n := range keys {
				if target != dataAddr(v, ordinal) {
					continue
				}
				if n < 0 {
					return respReply("LOADING Valkey is loading the dataset in memory")
				}
				return respInt(n)
			}
			return respReply("ERR no key count configured for " + target)
		case "SENTINEL":
			if len(args) >= 2 && strings.EqualFold(args[1], "MASTER") {
				return respSentinelMasterAt(podFQDN(v, 1), 1)
			}
		}
		return clusterAnswer(2, args)
	}
}

// --- D5: the preconditions on the pod about to be deleted ---------------------------

// Pod-0 is the outgoing pod, pod-1 the new master with two replicas attached, pod-2 a
// synced replica. Each case varies what pod-0 and pod-1 hold and what pod-0 answers.
func TestReplaceRemainingPods_DeletesTheOutgoingPodOnlyWhenNothingIsLost(t *testing.T) {
	for _, tc := range []struct {
		name         string
		newMaster    int
		outgoing     int
		outgoingInfo *valkeyclient.ReplicationInfo
		deleted      bool
		reason       string
	}{
		{name: "new master with keys, outgoing pod a replica", newMaster: nonEmptyKeyCount, outgoing: 500,
			outgoingInfo: replicaInfo(), deleted: true},
		{name: "both empty", newMaster: 0, outgoing: 0, outgoingInfo: replicaInfo(), deleted: true},
		{name: "empty new master, outgoing pod holds keys", newMaster: 0, outgoing: 500,
			outgoingInfo: replicaInfo(), reason: vkov1.ReasonDatasetWouldBeDiscarded},
		{name: "empty new master, outgoing count unreadable", newMaster: 0, outgoing: unreadableKeys,
			outgoingInfo: replicaInfo(), reason: vkov1.ReasonDatasetWouldBeDiscarded},
		{name: "new master count unreadable", newMaster: unreadableKeys, outgoing: 500,
			outgoingInfo: replicaInfo(), reason: vkov1.ReasonDatasetWouldBeDiscarded},
		{name: "new master with keys, outgoing count unreadable", newMaster: nonEmptyKeyCount, outgoing: unreadableKeys,
			outgoingInfo: replicaInfo(), deleted: true},
		{name: "new master with keys, outgoing pod still master", newMaster: nonEmptyKeyCount, outgoing: 500,
			outgoingInfo: masterInfo(0), reason: vkov1.ReasonFormerMasterStillMaster},
		{name: "new master with keys, outgoing pod does not answer", newMaster: nonEmptyKeyCount, outgoing: 500,
			outgoingInfo: nil, deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "hop"
			r, c, v, pods := midFailoverCluster(t, name, map[string]string{
				annotationRollingUpdateState: stateFailoverTriggered,
				// Past the budget, so a refusal is reported and its reason is readable.
				annotationHandoverHoldStarted: rfc3339Ago(time.Hour),
			}, nil)
			router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: tc.outgoing, 1: tc.newMaster}))
			router.attach(r)
			infos := map[string]*valkeyclient.ReplicationInfo{
				name + "-1": masterInfo(2),
				name + "-2": replicaInfo(),
			}
			if tc.outgoingInfo != nil {
				infos[name+"-0"] = tc.outgoingInfo
			}
			r.InstanceChecker = &perPodMockChecker{infos: infos}

			result := r.replaceRemainingPods(context.Background(), v, pods)

			require.NoError(t, result.Error)
			stored := crGet(t, c, name)
			cond := meta.FindStatusCondition(stored.Status.Conditions, vkov1.ConditionTypeMasterHandoverStalled)
			if tc.deleted {
				assert.False(t, podExists(t, c, name+"-0"), "nothing is lost, so the outgoing pod is replaced")
				assert.Equal(t, stateReplacingMaster, stored.Annotations[annotationRollingUpdateState])
				assert.NotContains(t, stored.Annotations, annotationHandoverHoldStarted,
					"the delete that goes through ends the gate's wait")
				assert.Nil(t, cond, "a handover that never stalled never gains the condition")
				return
			}
			assert.True(t, podExists(t, c, name+"-0"), "the outgoing pod is held")
			assert.Equal(t, stateFailoverTriggered, stored.Annotations[annotationRollingUpdateState],
				"the hold keeps the rolling-update state")
			require.NotNil(t, cond)
			assert.Equal(t, metav1.ConditionTrue, cond.Status)
			assert.Equal(t, tc.reason, cond.Reason)
			assert.Contains(t, cond.Message, name+"-0")
		})
	}
}

// --- D6: the hold and its report ----------------------------------------------------

// Inside syncTimeout a refusal is a plain requeue: nothing is reported yet, and the
// gate arms its own sync-wait bound.
func TestReplaceRemainingPods_HeldHandoverRequeuesInsideTheSyncTimeout(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hold-young", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
	}, nil)
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 500, 1: 0}))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hold-young-1": masterInfo(2),
		"hold-young-2": replicaInfo(),
	}}
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	result := r.replaceRemainingPods(context.Background(), v, pods)

	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Zero(t, result.DeferredRequeueAfter)
	stored := crGet(t, c, "hold-young")
	assert.NotEmpty(t, stored.Annotations[annotationHandoverHoldStarted], "the gate arms its own bound")
	assert.Nil(t, meta.FindStatusCondition(stored.Status.Conditions, vkov1.ConditionTypeMasterHandoverStalled))
	assert.Empty(t, rec.all())
	assert.True(t, podExists(t, c, "hold-young-0"))
}

// Past syncTimeout the hold is reported once and the pass continues: the state is
// kept, nothing is paused, nothing is deleted, one Warning is recorded, and the result
// defers instead of ending the pass -- the shape that holds the Sentinel roll.
func TestReplaceRemainingPods_HeldHandoverIsReportedPastTheSyncTimeout(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hold-old", map[string]string{
		annotationRollingUpdateState:  stateFailoverTriggered,
		annotationHandoverHoldStarted: rfc3339Ago(time.Hour),
	}, nil)
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 500, 1: 0}))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hold-old-1": masterInfo(2),
		"hold-old-2": replicaInfo(),
	}}
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	result := r.replaceRemainingPods(context.Background(), v, pods)

	assert.False(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter)
	stored := crGet(t, c, "hold-old")
	assert.Equal(t, stateFailoverTriggered, stored.Annotations[annotationRollingUpdateState])
	assert.Nil(t, meta.FindStatusCondition(stored.Status.Conditions, vkov1.ConditionTypeRollingUpdatePaused),
		"the hold is not a pause")
	cond := meta.FindStatusCondition(stored.Status.Conditions, vkov1.ConditionTypeMasterHandoverStalled)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonDatasetWouldBeDiscarded, cond.Reason)
	assert.Contains(t, cond.Message, "hold-old-1 holds no keys while hold-old-0 holds 500")
	assert.Contains(t, cond.Message, "SENTINEL MONITOR", "the message names the repair")
	assert.True(t, podExists(t, c, "hold-old-0"))
	require.Len(t, rec.withReason("MasterHandoverStalled"), 1)
	assert.Equal(t, corev1.EventTypeWarning, rec.withReason("MasterHandoverStalled")[0].eventType)

	// The next pass over the same state records no second Warning.
	r.replaceRemainingPods(context.Background(), crGet(t, c, "hold-old"), pods)
	assert.Len(t, rec.withReason("MasterHandoverStalled"), 1, "one Warning at the set, not one per pass")
}

// Once the refusal is gone the delete goes through, and the standing report and the
// wait's bound go with it.
func TestReplaceRemainingPods_ClearsTheHoldWhenTheDeleteGoesThrough(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hold-end", map[string]string{
		annotationRollingUpdateState:  stateFailoverTriggered,
		annotationHandoverHoldStarted: rfc3339Ago(time.Hour),
	}, nil)
	keys := map[int]int{0: 500, 1: 0}
	router := newRESPRouter(t, func(target string, args []string) string {
		return keysPerOrdinal(v, keys)(target, args)
	})
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hold-end-1": masterInfo(2),
		"hold-end-2": replicaInfo(),
	}}

	r.replaceRemainingPods(context.Background(), v, pods)
	require.True(t, meta.IsStatusConditionTrue(crGet(t, c, "hold-end").Status.Conditions,
		vkov1.ConditionTypeMasterHandoverStalled), "premise: the handover is held and reported")

	keys[1] = nonEmptyKeyCount // the first write reached the new master
	result := r.replaceRemainingPods(context.Background(), crGet(t, c, "hold-end"), pods)

	require.NoError(t, result.Error)
	assert.False(t, podExists(t, c, "hold-end-0"))
	stored := crGet(t, c, "hold-end")
	cond := meta.FindStatusCondition(stored.Status.Conditions, vkov1.ConditionTypeMasterHandoverStalled)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonMasterHandoverNotHeld, cond.Reason)
	assert.NotContains(t, stored.Annotations, annotationHandoverHoldStarted)
}

// A replica on the current spec that has not completed its sync holds the delete with
// its own reason.
func TestReplaceRemainingPods_ReportsAReplicaThatIsNotSynced(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hold-sync", map[string]string{
		annotationRollingUpdateState:  stateFailoverTriggered,
		annotationHandoverHoldStarted: rfc3339Ago(time.Hour),
	}, nil)
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 500, 1: nonEmptyKeyCount}))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hold-sync-1": masterInfo(2),
		"hold-sync-2": {Role: "slave", MasterLinkStatus: "down", MasterSyncInProgress: true},
	}}

	r.replaceRemainingPods(context.Background(), v, pods)

	cond := meta.FindStatusCondition(crGet(t, c, "hold-sync").Status.Conditions,
		vkov1.ConditionTypeMasterHandoverStalled)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonReplicaNotSynced, cond.Reason)
	assert.Contains(t, cond.Message, "hold-sync-2")
	assert.True(t, podExists(t, c, "hold-sync-0"))
}

// clearRollingUpdateState ends a hold too -- and only over a standing report.
func TestClearRollingUpdateState_ClearsAStandingHandoverHold(t *testing.T) {
	r, c, v, _ := midFailoverCluster(t, "crs-hold", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
	}, nil)
	r.setStatusCondition(context.Background(), v, vkov1.ConditionTypeMasterHandoverStalled,
		metav1.ConditionTrue, vkov1.ReasonReplicaNotSynced, "held")

	require.NoError(t, r.clearRollingUpdateState(context.Background(), crGet(t, c, "crs-hold")))

	cond := meta.FindStatusCondition(crGet(t, c, "crs-hold").Status.Conditions,
		vkov1.ConditionTypeMasterHandoverStalled)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
}

func TestClearRollingUpdateState_DoesNotAddTheHandoverCondition(t *testing.T) {
	r, c, _, _ := midFailoverCluster(t, "crs-nohold", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
	}, nil)

	require.NoError(t, r.clearRollingUpdateState(context.Background(), crGet(t, c, "crs-nohold")))

	assert.Nil(t, meta.FindStatusCondition(crGet(t, c, "crs-nohold").Status.Conditions,
		vkov1.ConditionTypeMasterHandoverStalled), "presence-guarded: no condition is invented")
}

// --- D4: the veto at forceReplicaConnections ----------------------------------------

// Pod-1 is the master REPLICAOF would point at; pod-0 and pod-2 are the targets.
func TestForceReplicaConnections_SendsNothingThatWouldDiscardTheOnlyDataset(t *testing.T) {
	for _, tc := range []struct {
		name     string
		keys     map[int]int
		notReady int // ordinal of a target that is not Ready, -1 for none
		targets  []int
	}{
		{name: "master with keys", keys: map[int]int{0: 500, 1: nonEmptyKeyCount, 2: 500},
			notReady: -1, targets: []int{0, 2}},
		{name: "every pod empty", keys: map[int]int{0: 0, 1: 0, 2: 0}, notReady: -1, targets: []int{0, 2}},
		{name: "empty master, a target holds keys", keys: map[int]int{0: 500, 1: 0, 2: 0},
			notReady: -1, targets: nil},
		{name: "empty master, the holder is not Ready", keys: map[int]int{0: 500, 1: 0, 2: 0},
			notReady: 0, targets: nil},
		{name: "empty master, a count is unreadable", keys: map[int]int{0: 0, 1: 0, 2: unreadableKeys},
			notReady: -1, targets: nil},
		{name: "master count unreadable", keys: map[int]int{0: 500, 1: unreadableKeys, 2: 500},
			notReady: -1, targets: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, v, pods := midFailoverCluster(t, "frc", nil, nil)
			if tc.notReady >= 0 {
				pods[tc.notReady].readyCondition = false
			}
			router := newRESPRouter(t, keysPerOrdinal(v, tc.keys))
			router.attach(r)

			r.forceReplicaConnections(context.Background(), v, pods[1].name, pods)

			want := []string{}
			for _, ordinal := range tc.targets {
				want = append(want, dataAddr(v, ordinal))
			}
			assert.ElementsMatch(t, want, router.targetsFor("REPLICAOF"))
		})
	}
}

// A per-target veto would point the empty pod at the empty master and spare the
// holder -- and the empty master would then have a replica, which satisfies every
// gate behind it and unlocks the holder's delete. The whole call is vetoed.
func TestForceReplicaConnections_TheVetoCoversTheWholeCall(t *testing.T) {
	r, _, v, pods := midFailoverCluster(t, "frc-all", nil, nil)
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 500, 1: 0, 2: 0}))
	router.attach(r)

	r.forceReplicaConnections(context.Background(), v, pods[1].name, pods)

	assert.Empty(t, router.targetsFor("REPLICAOF"), "not even the empty pod is re-pointed")
	assert.Equal(t, []string{dataAddr(v, 1)}, router.targetsFor("DBSIZE")[:1],
		"the master is counted first")
}

// --- the refusal shape, end to end over a pass -----------------------------------

// The ADR 0028 refusal shape on the Sentinel path: Sentinel names the new master, which
// holds no keys, while the outgoing pod still answers master and holds the dataset. The
// failover stamp is older than the ADR 0025 D9 window, so the resolver runs, and the
// no-replica branch has timed out, so forceReplicaConnections runs. Neither may send the
// holder a REPLICAOF; and once the new master has a replica, the holder is still not
// deleted.
func TestHandleRollingUpdate_TheRefusalShapeKeepsTheDataset(t *testing.T) {
	name := "shape"
	r, c, v, sts := haRollingUpdate(t, name, map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(replicaReconnectTimeout + time.Minute),
	}, []int{0}, nil)
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 500, 1: 0, 2: 0}))
	router.attach(r)
	infos := map[string]*valkeyclient.ReplicationInfo{
		name + "-0": masterInfo(0), // the outgoing pod, still master, holding the data
		name + "-1": masterInfo(0), // Sentinel's master: promoted, empty, no replica yet
		name + "-2": replicaInfo(),
	}
	r.InstanceChecker = &perPodMockChecker{infos: infos}

	result := r.handleRollingUpdate(context.Background(), v, sts)

	require.NoError(t, result.Error)
	assert.NotContains(t, router.targetsFor("REPLICAOF"), dataAddr(v, 0),
		"neither the resolver nor forceReplicaConnections may flush the pod holding the data")
	assert.True(t, podExists(t, c, name+"-0"))

	// The new master gains a replica: the gate's attachment check passes, and only the
	// dataset veto stands between the holder and its delete.
	infos[name+"-1"] = masterInfo(1)
	result = r.handleRollingUpdate(context.Background(), crGet(t, c, name), sts)

	require.NoError(t, result.Error)
	assert.True(t, podExists(t, c, name+"-0"), "the pod holding the only dataset is not deleted")
	assert.NotContains(t, router.targetsFor("REPLICAOF"), dataAddr(v, 0))
	assert.Equal(t, fmt.Sprintf("SENTINEL MASTER %s", builder.SentinelMonitorName(v)),
		router.commandsTo(sentinelAddr(v, 0, builder.SentinelPort))[0], "premise: Sentinel was asked")
}

// --- review fixes: reason precedence, the hold's own clock, the absence clock --------

// In the shape the REPLICAOF veto leaves, the new master has no replica attached, so
// the replica check refuses as well; the dataset veto is the reason reported, because
// it is the one with the repair and nothing else on the CR shows it.
func TestReplaceRemainingPods_ReportsTheDatasetVetoBeforeAMissingReplica(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "hold-first", map[string]string{
		annotationRollingUpdateState:  stateFailoverTriggered,
		annotationHandoverHoldStarted: rfc3339Ago(time.Hour),
	}, nil)
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 500, 1: 0}))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"hold-first-1": masterInfo(0),
		"hold-first-2": replicaInfo(),
	}}

	r.replaceRemainingPods(context.Background(), v, pods)

	cond := meta.FindStatusCondition(crGet(t, c, "hold-first").Status.Conditions,
		vkov1.ConditionTypeMasterHandoverStalled)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonDatasetWouldBeDiscarded, cond.Reason)
	assert.Contains(t, cond.Message, "SENTINEL MONITOR")
}

// The refusal shape reached from replacing-replicas: Sentinel names the empty current
// pod, the resolver refuses to demote the outgoing one, and replaceNextReplica finds
// no replica left to replace -- after verifyReplacedReplicasSynced has cleared the
// sync-wait bound. The hold keeps its own clock, so it is still reported.
func TestHandleRollingUpdate_TheHoldKeepsItsClockOutsideTheFailoverState(t *testing.T) {
	name := "hold-clock"
	r, c, v, sts := haRollingUpdate(t, name, map[string]string{
		annotationRollingUpdateState:  stateReplacingReplicas,
		annotationHandoverHoldStarted: rfc3339Ago(time.Hour),
	}, []int{0}, nil)
	router := newRESPRouter(t, keysPerOrdinal(v, map[int]int{0: 500, 1: 0, 2: 0}))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		name + "-0": masterInfo(0),
		name + "-1": masterInfo(1),
		name + "-2": replicaInfo(),
	}}

	result := r.handleRollingUpdate(context.Background(), v, sts)

	require.NoError(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.DeferredRequeueAfter,
		"past its budget the hold continues the pass")
	assert.False(t, result.NeedsRequeue)
	assert.True(t, podExists(t, c, name+"-0"))
	cond := meta.FindStatusCondition(crGet(t, c, name).Status.Conditions, vkov1.ConditionTypeMasterHandoverStalled)
	require.NotNil(t, cond)
	assert.Equal(t, vkov1.ReasonDatasetWouldBeDiscarded, cond.Reason)
}

// One pass that finds no master -- the new master not Ready, its INFO timing out --
// is not a failover that produced none: with the trigger's stamp long expired it
// used to reset Sentinel and force a failover of a healthy master. The absence has to
// last failoverRetryTimeout of its own.
func TestHandleNoMasterFound_OnePassWithoutAMasterIsNotATimeout(t *testing.T) {
	r, c, v, pods := midFailoverCluster(t, "nm-once", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(time.Hour),
	}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleNoMasterFound(context.Background(), v, pods)

	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	assert.Empty(t, router.sent(), "Sentinel is not reset on the first pass that misses the master")
	assert.Equal(t, stateFailoverTriggered, crGet(t, c, "nm-once").Annotations[annotationRollingUpdateState])

	noMasterFor(r, v, failoverRetryTimeout+time.Second)
	result = r.handleNoMasterFound(context.Background(), crGet(t, c, "nm-once"), pods)

	require.NoError(t, result.Error)
	assert.Equal(t, stateFailoverReset, crGet(t, c, "nm-once").Annotations[annotationRollingUpdateState],
		"an absence that lasts is still reset, so the wait stays bounded")
}

// A pass that finds the master ends the absence: a later pass that misses it starts
// counting again.
func TestHandlePostFailover_AFoundMasterEndsTheAbsence(t *testing.T) {
	r, _, v, _ := haRollingUpdate(t, "nm-found", map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  rfc3339Ago(time.Hour),
	}, []int{0}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"nm-found-1": masterInfo(0),
		"nm-found-2": replicaInfo(),
	}}
	noMasterFor(r, v, time.Hour)

	r.handlePostFailover(context.Background(), v, nil, 0)

	_, tracked := r.nudges.firstSeen(waitBoundKey(v.Namespace, v.Name, boundNoMaster))
	assert.False(t, tracked, "the absence clock is dropped once a master answers")
}
