package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// countingValkeyServer answers like fakeValkeyServerWithKeys (a readable DBSIZE, so
// the ADR 0028 dataset veto lets a demotion through) and counts the
// REPLICAOF commands it receives -- the one command a demotion sends.
func countingValkeyServer(t *testing.T, replicaof *atomic.Int32) string {
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
				switch {
				case strings.Contains(request, "REPLICAOF"):
					replicaof.Add(1)
					_, _ = conn.Write([]byte("+OK\r\n"))
				case strings.Contains(request, "WAIT"):
					_, _ = conn.Write([]byte(":1\r\n"))
				case strings.Contains(request, "DBSIZE"):
					_, _ = fmt.Fprintf(conn, ":%d\r\n", nonEmptyKeyCount)
				default:
					_, _ = conn.Write([]byte("+OK\r\n"))
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// TestHandleRollingUpdate_DoesNotDemoteTheReplicaSentinelIsPromoting: while a
// Sentinel failover the roll requested is in flight, the replica Sentinel promotes
// answers master before Sentinel moves its master pointer, and demoting it undoes
// the failover the operator asked for -- measured as a reset-and-retrigger cycle of
// over ten minutes on Kind. In that state the double master is reported, not
// resolved; in every other state it is resolved as before.
//
// Revert check: calling resolveSplitBrain unconditionally in handleRollingUpdate
// fails the "just armed" row (a REPLICAOF is sent); dropping the clock from
// ownFailoverInFlight fails the "past the window" row, dropping the timestamp
// presence check the "no timestamp" row.
func TestHandleRollingUpdate_DoesNotDemoteTheReplicaSentinelIsPromoting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		timestamp string
		demotion  bool
	}{
		{"failover-triggered, just armed", stateFailoverTriggered, rfc3339Ago(time.Second), false},
		// The positive control: the same double master outside the failover
		// window is resolved, so the fixture can observe a demotion at all.
		{"failover-reset", stateFailoverReset, rfc3339Ago(time.Second), true},
		// The window's own clock: past replicaReconnectTimeout Sentinel's pointer
		// is settled, whatever the post-failover handler still waits for.
		{"failover-triggered, past the window", stateFailoverTriggered, rfc3339Ago(replicaReconnectTimeout + time.Minute), true},
		// A state an earlier operator wrote without its timestamp is no window.
		{"failover-triggered, no timestamp", stateFailoverTriggered, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("sb-fo", "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.Image = "valkey/valkey:9.0"
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
				v.Annotations = map[string]string{annotationRollingUpdateState: tc.state}
				if tc.timestamp != "" {
					v.Annotations[annotationFailoverTimestamp] = tc.timestamp
				}
			})
			// pod-0: the old master, still on the old image. pod-1: updated, the
			// replica Sentinel is promoting, already answering master. pod-2: updated
			// replica.
			pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
			pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
			pod1 := createPodForSts(v, 1, "valkey/valkey:9.0", true)
			pod2 := createPodForSts(v, 2, "valkey/valkey:9.0", true)
			r, _ := newTestReconciler(v, pod0, pod1, pod2)

			var replicaof atomic.Int32
			addr := countingValkeyServer(t, &replicaof)
			r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client { return valkeyclient.New(addr) }
			r.InstanceChecker = &mockInstanceChecker{
				replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
					switch podName {
					case "sb-fo-0":
						return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 1}, nil
					case "sb-fo-1":
						return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
					default:
						return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
					}
				},
			}
			reconcileOnce(t, r, "sb-fo", "default")
			replicaof.Store(0) // only what handleRollingUpdate does counts

			sts := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-fo", Namespace: "default"}, sts))
			cr := &vkov1.Valkey{}
			require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-fo", Namespace: "default"}, cr))
			cr.Annotations[annotationRollingUpdateState] = tc.state
			if tc.timestamp != "" {
				cr.Annotations[annotationFailoverTimestamp] = tc.timestamp
			} else {
				delete(cr.Annotations, annotationFailoverTimestamp)
			}
			r.handleRollingUpdate(context.Background(), cr, sts)

			if tc.demotion {
				assert.Positive(t, replicaof.Load(), "outside the failover window the rogue master is demoted")
				return
			}
			assert.Zero(t, replicaof.Load(), "the replica Sentinel is promoting must not be demoted")
			require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sb-fo", Namespace: "default"}, cr))
			assert.True(t, meta.IsStatusConditionTrue(cr.Status.Conditions, vkov1.ConditionTypeMultipleMasters),
				"the double master is still reported: %v", cr.Status.Conditions)
		})
	}
}

// TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp: the write that
// enters stateFailoverTriggered carries the failover timestamp. Written separately,
// a failed second write left the state unbounded -- a missing timestamp never
// expires -- and ADR 0025 D9 skips the split-brain resolution for exactly as long
// as that state stands.
//
// Revert check: splitting setFailoverTriggered back into setRollingUpdateState
// followed by setFailoverTimestamp fails the assertion inside the interceptor.
func TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp(t *testing.T) {
	var sawTrigger atomic.Bool
	var armedTogether atomic.Bool
	funcs := interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if cr, ok := obj.(*vkov1.Valkey); ok &&
				cr.Annotations[annotationRollingUpdateState] == stateFailoverTriggered && !sawTrigger.Load() {
				sawTrigger.Store(true)
				armedTogether.Store(cr.Annotations[annotationFailoverTimestamp] != "")
			}
			return c.Update(ctx, obj, opts...)
		},
	}
	r, _, v, sts := haRollingUpdate(t, "arm-fo", map[string]string{
		annotationRollingUpdateState: stateReplacingReplicas,
	}, []int{0}, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)
	r.InstanceChecker = &perPodMockChecker{infos: map[string]*valkeyclient.ReplicationInfo{
		"arm-fo-0": masterInfo(2),
		"arm-fo-1": replicaInfo(),
		"arm-fo-2": replicaInfo(),
	}}

	result := r.handleRollingUpdate(context.Background(), v, sts)
	require.NoError(t, result.Error)
	require.True(t, sawTrigger.Load(), "premise: the pass enters stateFailoverTriggered")
	assert.True(t, armedTogether.Load(), "the state must be written together with its timestamp")
}

// TestHandleFailoverRetrigger_ArmsTheFailoverStateWithAFreshTimestamp is the
// retrigger half: its first write that names stateFailoverTriggered must carry a
// new stamp, not the one the reset left. Written separately, the retriggered
// failover ran on the reset's stamp -- at least failoverResetMinWait old -- and its
// timeouts fired early.
//
// Revert check: splitting setFailoverTriggered back into two writes in
// handleFailoverRetrigger alone fails this test (the state arrives with the old
// stamp); TestHandleRollingUpdate_ArmsTheFailoverStateWithItsTimestamp covers the
// other site.
func TestHandleFailoverRetrigger_ArmsTheFailoverStateWithAFreshTimestamp(t *testing.T) {
	before := rfc3339Ago(failoverResetMinWait + time.Minute)
	var sawTrigger, freshStamp atomic.Bool
	funcs := interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if cr, ok := obj.(*vkov1.Valkey); ok &&
				cr.Annotations[annotationRollingUpdateState] == stateFailoverTriggered && !sawTrigger.Load() {
				sawTrigger.Store(true)
				stamp := cr.Annotations[annotationFailoverTimestamp]
				freshStamp.Store(stamp != "" && stamp != before)
			}
			return c.Update(ctx, obj, opts...)
		},
	}
	r, _, v, _ := midFailoverCluster(t, "arm-rt", map[string]string{
		annotationRollingUpdateState: stateFailoverReset,
		annotationFailoverTimestamp:  before,
	}, &funcs)
	router := newRESPRouter(t, healthyCluster(2))
	router.attach(r)

	result := r.handleFailoverRetrigger(context.Background(), v)
	require.NoError(t, result.Error)
	require.True(t, sawTrigger.Load(), "premise: the retrigger enters stateFailoverTriggered")
	assert.True(t, freshStamp.Load(), "the retriggered state must arrive with its own timestamp")
}
