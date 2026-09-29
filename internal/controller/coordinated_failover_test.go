package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The roll's own Sentinel failover is coordinated where Sentinel supports it and
// forced otherwise; the retrigger stays forced
// (docs/adr/0037-the-master-handover-loses-no-acknowledged-write-and-no-dataset.md, D1).
//
// The replies are the ones the two Valkey lines give, measured in docker on
// valkey/valkey:9.1.1 and 8.1.9 (2026-09-28): 9.1.1 answers COORDINATED with OK, an
// unknown option with "ERR Unknown failover option specified" and a second request
// with "INPROG Failover already in progress"; 8.1.9 answers COORDINATED with
// "ERR wrong number of arguments for 'sentinel|failover' command".

const (
	replyOptionUnknownToValkey8 = "ERR wrong number of arguments for 'sentinel|failover' command"
	replyOptionUnknownToValkey9 = "ERR Unknown failover option specified"
	replyNoGoodPrimary          = "NOGOODPRIMARY Primary does not support FAILOVER command"
	replyInProgress             = "INPROG Failover already in progress"
	replyNoGoodReplica          = "NOGOODSLAVE No suitable replica to promote"
	replyNoSuchMaster           = "ERR No such master with that name"
)

// respReply is an error reply with its own code, unlike respErr, which prefixes ERR.
func respReply(reply string) string { return "-" + reply + "\r\n" }

// failoverCommandsTo returns the SENTINEL FAILOVER commands that reached one
// endpoint, in order.
func failoverCommandsTo(router *respRouter, target string) []string {
	out := []string{}
	for _, cmd := range router.commandsTo(target) {
		if strings.HasPrefix(cmd, "SENTINEL FAILOVER") {
			out = append(out, cmd)
		}
	}
	return out
}

// failoverReplies answers SENTINEL FAILOVER per sentinel ordinal: coordinated holds
// the replies to the COORDINATED command, forced the replies to the plain one. An
// ordinal missing from a table accepts with OK. Every other command gets the healthy
// cluster answer.
func failoverReplies(v *vkov1.Valkey, coordinated, forced map[int]string) func(string, []string) string {
	return func(target string, args []string) string {
		if len(args) < 3 || !strings.EqualFold(args[0], "SENTINEL") || !strings.EqualFold(args[1], "FAILOVER") {
			return clusterAnswer(2, args)
		}
		table := forced
		if len(args) == 4 && strings.EqualFold(args[3], "COORDINATED") {
			table = coordinated
		}
		for ordinal, reply := range table {
			if target == sentinelAddr(v, ordinal, builder.SentinelPort) {
				return respReply(reply)
			}
		}
		return respOK
	}
}

func coordinatedCmd(v *vkov1.Valkey) string {
	return "SENTINEL FAILOVER " + builder.SentinelMonitorName(v) + " COORDINATED"
}

func forcedCmd(v *vkov1.Valkey) string {
	return "SENTINEL FAILOVER " + builder.SentinelMonitorName(v)
}

func TestTriggerSentinelFailover_CoordinatedWhereSentinelSupportsIt(t *testing.T) {
	v := sentinelClusterCR("tsf-coord", 3)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, failoverReplies(v, nil, nil))
	router.attach(r)

	require.NoError(t, r.triggerSentinelFailover(context.Background(), v, true))

	assert.Equal(t, []string{coordinatedCmd(v)}, failoverCommandsTo(router, sentinelAddr(v, 0, builder.SentinelPort)),
		"a Sentinel that accepts COORDINATED is never sent the forced command")
	assert.Empty(t, failoverCommandsTo(router, sentinelAddr(v, 1, builder.SentinelPort)),
		"the first Sentinel that accepts ends the trigger")
}

// A Sentinel that refuses the option itself is asked with the forced command at once:
// it is the Sentinel that answered, and its answer says the tier cannot coordinate.
func TestTriggerSentinelFailover_FallsBackToForcedOnTheSentinelThatRefusedTheOption(t *testing.T) {
	for name, refusal := range map[string]string{
		"a Valkey 8 Sentinel":             replyOptionUnknownToValkey8,
		"a Valkey 9 Sentinel, bad option": replyOptionUnknownToValkey9,
		"a master without FAILOVER":       replyNoGoodPrimary,
	} {
		t.Run(name, func(t *testing.T) {
			v := sentinelClusterCR("tsf-fallback", 3)
			r, _ := newTestReconciler(v)
			router := newRESPRouter(t, failoverReplies(v, map[int]string{0: refusal}, nil))
			router.attach(r)

			require.NoError(t, r.triggerSentinelFailover(context.Background(), v, true))

			assert.Equal(t, []string{coordinatedCmd(v), forcedCmd(v)},
				failoverCommandsTo(router, sentinelAddr(v, 0, builder.SentinelPort)),
				"the Sentinel that refused COORDINATED is the one asked for the forced failover")
			assert.Empty(t, failoverCommandsTo(router, sentinelAddr(v, 1, builder.SentinelPort)))
		})
	}
}

// INPROG and NOGOODSLAVE refuse the failover, not the option. They stay a failed
// attempt at that Sentinel: nothing forced is sent, and the next Sentinel is asked in
// the same mode. So does an ERR that is not about the option -- matching ERR alone
// would send a tier that can coordinate into the forced failover over a Sentinel
// that merely does not know the monitor name.
func TestTriggerSentinelFailover_ARefusedFailoverIsAFailedAttemptNotAFallback(t *testing.T) {
	for name, refusal := range map[string]string{
		"failover in progress":  replyInProgress,
		"no good replica":       replyNoGoodReplica,
		"an unrelated ERR text": replyNoSuchMaster,
	} {
		t.Run(name, func(t *testing.T) {
			v := sentinelClusterCR("tsf-failed", 3)
			r, _ := newTestReconciler(v)
			router := newRESPRouter(t, failoverReplies(v, map[int]string{0: refusal}, nil))
			router.attach(r)

			require.NoError(t, r.triggerSentinelFailover(context.Background(), v, true))

			assert.Equal(t, []string{coordinatedCmd(v)}, failoverCommandsTo(router, sentinelAddr(v, 0, builder.SentinelPort)),
				"a refused failover is not answered with a forced one")
			assert.Equal(t, []string{coordinatedCmd(v)}, failoverCommandsTo(router, sentinelAddr(v, 1, builder.SentinelPort)),
				"the next Sentinel is asked in the same mode")
		})
	}
}

// Once a Sentinel refused the option, the rest of the pass is forced: the Sentinel
// tier shares one image, and a second COORDINATED would buy the same refusal.
func TestTriggerSentinelFailover_NoSecondCoordinatedAttemptAfterAFallback(t *testing.T) {
	v := sentinelClusterCR("tsf-once", 3)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, failoverReplies(v,
		map[int]string{0: replyOptionUnknownToValkey8, 1: replyOptionUnknownToValkey8},
		map[int]string{0: replyInProgress}))
	router.attach(r)

	require.NoError(t, r.triggerSentinelFailover(context.Background(), v, true))

	assert.Equal(t, []string{coordinatedCmd(v), forcedCmd(v)},
		failoverCommandsTo(router, sentinelAddr(v, 0, builder.SentinelPort)))
	assert.Equal(t, []string{forcedCmd(v)}, failoverCommandsTo(router, sentinelAddr(v, 1, builder.SentinelPort)),
		"after a fallback the next Sentinel is asked forced only")
}

func TestTriggerSentinelFailover_ForcedWhenNotCoordinated(t *testing.T) {
	v := sentinelClusterCR("tsf-forced", 3)
	r, _ := newTestReconciler(v)
	router := newRESPRouter(t, failoverReplies(v, nil, nil))
	router.attach(r)

	require.NoError(t, r.triggerSentinelFailover(context.Background(), v, false))

	assert.Equal(t, []string{forcedCmd(v)}, failoverCommandsTo(router, sentinelAddr(v, 0, builder.SentinelPort)))
}

// Every Sentinel refusing is still an error the post-failover handler retries from.
func TestTriggerSentinelFailover_AllRefusedIsAnError(t *testing.T) {
	v := sentinelClusterCR("tsf-none", 3)
	r, _ := newTestReconciler(v)
	all := map[int]string{0: replyInProgress, 1: replyInProgress, 2: replyInProgress}
	router := newRESPRouter(t, failoverReplies(v, all, all))
	router.attach(r)

	err := r.triggerSentinelFailover(context.Background(), v, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "INPROG")
	for ordinal := 0; ordinal < 3; ordinal++ {
		assert.Equal(t, []string{coordinatedCmd(v)},
			failoverCommandsTo(router, sentinelAddr(v, ordinal, builder.SentinelPort)))
	}
}

// The retrigger stays forced: it runs after a failover that promoted nothing, and a
// coordinated one that keeps losing its election would loop.
func TestHandleFailoverRetrigger_StaysForced(t *testing.T) {
	r, _, v, _ := midFailoverCluster(t, "hfr-forced", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  rfc3339Ago(failoverResetMinWait + time.Minute),
	}, nil)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)

	require.NoError(t, result.Error)
	assert.Equal(t, []string{forcedCmd(v)}, failoverCommandsTo(router, sentinelAddr(v, 0, builder.SentinelPort)))
}

func TestCoordinatedFallbackReason(t *testing.T) {
	wrap := func(reply string) error {
		return fmt.Errorf("sentinel failover m coordinated on x: %w", &valkeyclient.ReplyError{Message: reply})
	}
	assert.Equal(t, replyOptionUnknownToValkey8, coordinatedFallbackReason(failoverModeCoordinated, wrap(replyOptionUnknownToValkey8)))
	assert.Equal(t, replyOptionUnknownToValkey9, coordinatedFallbackReason(failoverModeCoordinated, wrap(replyOptionUnknownToValkey9)))
	assert.Equal(t, replyNoGoodPrimary, coordinatedFallbackReason(failoverModeCoordinated, wrap(replyNoGoodPrimary)))
	assert.Empty(t, coordinatedFallbackReason(failoverModeCoordinated, wrap(replyInProgress)))
	assert.Empty(t, coordinatedFallbackReason(failoverModeCoordinated, wrap(replyNoGoodReplica)))
	assert.Empty(t, coordinatedFallbackReason(failoverModeCoordinated, wrap(replyNoSuchMaster)))
	assert.Empty(t, coordinatedFallbackReason(failoverModeCoordinated, errors.New("cannot connect to x: refused")),
		"a transport error is no answer at all")
	assert.Empty(t, coordinatedFallbackReason(failoverModeForced, wrap(replyOptionUnknownToValkey8)),
		"a forced command has nothing to fall back to")
	assert.Empty(t, coordinatedFallbackReason(failoverModeCoordinated, nil))
}
