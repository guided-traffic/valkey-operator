package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// The tests in this file pin the invariant that ADR 0011 D1 (checkSteadyStateSplitBrain)
// and ADR 0008 D8, D9 (the init-script self-claim) turned the known-master annotation into:
// it names the pod the operator last promoted, and a promotion the operator could
// not record is not a completed promotion.
//
// It matters because a demotion is a REPLICAOF, which discards the demoted pod's
// dataset. Every write path of the annotation therefore has to be as reliable as
// the destructive read that now depends on it.

// podHost is the FQDN the operator stores in the known-master annotation.
func podHost(name string, ordinal int) string {
	return fmt.Sprintf("%s-%d.%s-headless.%s.svc.cluster.local", name, ordinal, name, testNamespace)
}

// rejectKnownMasterWrite refuses exactly the CR write that would record host as the
// known master -- a fail-closed admission webhook on the CR, or any other permanent
// rejection. Every other write passes, so a pass that carries on after the failure
// is free to advance its state machine and can be observed doing so.
func rejectKnownMasterWrite(host string, attempts *int) interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if cr, isCR := obj.(*vkov1.Valkey); isCR &&
				cr.Annotations[builder.AnnotationKnownMaster] == host {
				*attempts++
				return apierrors.NewInternalError(fmt.Errorf("admission webhook denied the annotation"))
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
}

// dialAllOK points every Valkey client at one fake server that answers every
// command, and records the address the reconciler asked for.
func dialAllOK(t *testing.T, r *ValkeyReconciler, seen *[]string) {
	t.Helper()
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
		*seen = append(*seen, target)
		return valkeyclient.New(addr)
	}
}

// --- persistKnownMaster: the object must never carry an unpersisted authority ---

// A failed write leaves the API server holding the previous address. The in-memory
// object has to say the same thing, or the value that was never persisted is read
// back as authority later in the same pass.
func TestPersistKnownMaster_KeepsThePersistedValueWhenTheWriteFails(t *testing.T) {
	const name = "km-restore"
	previous := podHost(name, 1)
	rejected := podHost(name, 0)

	attempts := 0
	funcs := rejectKnownMasterWrite(rejected, &attempts)
	r, c, v, _ := multiReplicaFixture(t, name, map[string]string{
		builder.AnnotationKnownMaster: previous,
	}, &funcs)

	err := r.persistKnownMaster(context.Background(), v, rejected)

	require.Error(t, err, "a write that never landed must not be reported as success")
	require.Equal(t, 1, attempts)
	assert.Equal(t, previous, v.Annotations[builder.AnnotationKnownMaster],
		"the in-memory object must reflect what is persisted, not what was attempted")
	assert.Equal(t, previous, crGet(t, c, name).Annotations[builder.AnnotationKnownMaster])
}

// Same rule with nothing recorded before: the key must be gone, not left holding a
// value no reconcile will ever read from the API.
func TestPersistKnownMaster_DropsTheKeyItCouldNotCreate(t *testing.T) {
	const name = "km-drop"
	rejected := podHost(name, 0)

	attempts := 0
	funcs := rejectKnownMasterWrite(rejected, &attempts)
	r, _, v, _ := multiReplicaFixture(t, name, map[string]string{}, &funcs)

	err := r.persistKnownMaster(context.Background(), v, rejected)

	require.Error(t, err)
	require.Equal(t, 1, attempts)
	_, present := v.Annotations[builder.AnnotationKnownMaster]
	assert.False(t, present, "an annotation that was never written must not stay on the object")
}

// --- promotePod0AndRedirect: an unrecorded promotion must not advance Phase 1 ---

// The loss ADR 0011 D1 and ADR 0008 D8, D9 describe, reproduced at its origin:
// pod-0 is promoted, the annotation write is rejected, and the pass used to carry
// on into Phase 2 anyway. The state then cleared with pod-0 as master and the
// annotation still naming pod-1, and nothing could ever correct it --
// pod0SyncWaitReason rejects a pod-0 that already reports master, so this function
// never runs again, and clearRollingUpdateState deliberately keeps the annotation.
// The next restart of pod-1 self-claimed master and checkSteadyStateSplitBrain
// demoted pod-0, discarding every write since the failover.
func TestPromotePod0AndRedirect_DoesNotAdvanceWhenTheRecordFails(t *testing.T) {
	const name = "promote-blocked"
	promotedHost := podHost(name, 1)
	pod0Host := podHost(name, 0)

	attempts := 0
	funcs := rejectKnownMasterWrite(pod0Host, &attempts)
	r, c, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState:  stateRestoringTopology,
		annotationPromotedPod:         name + "-1",
		builder.AnnotationKnownMaster: promotedHost,
	}, &funcs)

	var dialed []string
	dialAllOK(t, r, &dialed)

	result := r.promotePod0AndRedirect(context.Background(), v, sts, name+"-0")

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue, "the pass has to come back, not report progress")
	assert.False(t, result.Completed)
	require.Positive(t, attempts, "the record must have been attempted")
	require.NotEmpty(t, dialed, "REPLICAOF NO ONE runs first, so pod-0 really is master now")

	final := crGet(t, c, name)
	assert.Equal(t, stateRestoringTopology, final.Annotations[annotationRollingUpdateState],
		"Phase 2 must not be entered on a promotion the operator could not record")
	assert.Equal(t, promotedHost, final.Annotations[builder.AnnotationKnownMaster],
		"the persisted authority stays the promoted pod, so the topology stays consolidatable")
	assert.Equal(t, promotedHost, v.Annotations[builder.AnnotationKnownMaster],
		"and the in-memory object must not disagree with it")
	assert.Nil(t, apimeta.FindStatusCondition(final.Status.Conditions, vkov1.ConditionTypeTopologyRestored),
		"no restoration verdict may be published for a promotion that is not recorded")
}

// The unblocked path still completes, so the guard above is not simply a stall.
func TestPromotePod0AndRedirect_AdvancesWhenTheRecordSucceeds(t *testing.T) {
	const name = "promote-ok"
	pod0Host := podHost(name, 0)

	r, c, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState:  stateRestoringTopology,
		annotationPromotedPod:         name + "-1",
		builder.AnnotationKnownMaster: podHost(name, 1),
	}, nil)

	var dialed []string
	dialAllOK(t, r, &dialed)

	result := r.promotePod0AndRedirect(context.Background(), v, sts, name+"-0")

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)

	final := crGet(t, c, name)
	assert.Equal(t, stateVerifyingTopology, final.Annotations[annotationRollingUpdateState])
	assert.Equal(t, pod0Host, final.Annotations[builder.AnnotationKnownMaster])
}

// --- checkAndRecoverNoMaster: promoting without recording is the same defect ---

// This path promotes pod-0 with REPLICAOF NO ONE on a completely healthy API
// server and used to record nothing at all. Whatever the annotation named before --
// a pod from an earlier failover -- stayed the authority, and this function never
// gets a second chance to fix it: the next pass finds a master and short-circuits.
func TestCheckAndRecoverNoMaster_RecordsThePromotion(t *testing.T) {
	const name = "no-master"
	staleHost := podHost(name, 2)
	pod0Host := podHost(name, 0)

	r, c, v, _ := multiReplicaFixture(t, name, map[string]string{
		builder.AnnotationKnownMaster: staleHost,
	}, nil)
	r.InstanceChecker = newRolesChecker(map[string]string{
		name + "-0": common.RoleReplica,
		name + "-1": common.RoleReplica,
		name + "-2": common.RoleReplica,
	})

	var dialed []string
	dialAllOK(t, r, &dialed)

	recovered, err := r.checkAndRecoverNoMaster(context.Background(), v)

	require.NoError(t, err)
	require.True(t, recovered)

	final := crGet(t, c, name)
	assert.Equal(t, pod0Host, final.Annotations[builder.AnnotationKnownMaster],
		"the promoted pod must become the recorded authority, or a stale name outlives it")

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.ReplicaConfigMapName(v), Namespace: testNamespace}, cm))
	assert.Contains(t, cm.Data[builder.ValkeyConfigKey], "replicaof "+pod0Host,
		"a replica that restarts before the next reconcile reads the mounted ConfigMap")
}

// The record is written before the promotion, so a rejected write leaves nothing
// promoted and the recovery simply runs again on the next pass.
func TestCheckAndRecoverNoMaster_PromotesNothingWhenTheRecordFails(t *testing.T) {
	const name = "no-master-blocked"
	pod0Host := podHost(name, 0)

	attempts := 0
	funcs := rejectKnownMasterWrite(pod0Host, &attempts)
	r, c, v, _ := multiReplicaFixture(t, name, map[string]string{}, &funcs)
	r.InstanceChecker = newRolesChecker(map[string]string{
		name + "-0": common.RoleReplica,
		name + "-1": common.RoleReplica,
		name + "-2": common.RoleReplica,
	})

	var dialed []string
	dialAllOK(t, r, &dialed)

	recovered, err := r.checkAndRecoverNoMaster(context.Background(), v)

	require.Error(t, err, "the caller has to requeue, not treat this as recovered")
	assert.False(t, recovered)
	require.Positive(t, attempts)
	assert.Empty(t, dialed, "nothing may be promoted while the promotion cannot be recorded")
	assert.Empty(t, crGet(t, c, name).Annotations[builder.AnnotationKnownMaster])
}

// --- promotePod0AndRedirect: an unrecorded promotion must not be left standing ---

// recordingValkeyServer answers every command with +OK and keeps the raw command
// text, so a test can assert which commands a pass actually issued rather than
// only which addresses it dialed. The client opens one connection per command.
func recordingValkeyServer(t *testing.T) (string, func() []string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var seen []string

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
				mu.Lock()
				seen = append(seen, string(buf[:n]))
				mu.Unlock()
				// Recorded before the reply, so a command is in the log by the
				// time the client that sent it returns.
				_, _ = conn.Write([]byte("+OK\r\n"))
			}()
		}
	}()

	return ln.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// wireRecordingServer points every Valkey client at one recording server.
func wireRecordingServer(t *testing.T, r *ValkeyReconciler) func() []string {
	t.Helper()
	addr, commands := recordingValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}
	return commands
}

// Not advancing the state machine is only half the fix. The REPLICAOF NO ONE has
// already run when the record fails, so a bare requeue leaves the cluster with a
// master the annotation does not name for up to the Phase 1 budget
// (spec.rollingUpdate.syncTimeout, default 5m) -- and every write the -rw Service
// sends to pod-0 in that window is discarded when Phase 2 demotes it back toward
// the promoted pod. Handing pod-0 back immediately collapses the window to zero
// and costs nothing: Phase 1 only promotes it once it has fully synced from that
// very pod.
func TestPromotePod0AndRedirect_RollsThePromotionBackWhenTheRecordFails(t *testing.T) {
	const name = "promote-rollback"
	promotedHost := podHost(name, 1)
	pod0Host := podHost(name, 0)

	attempts := 0
	funcs := rejectKnownMasterWrite(pod0Host, &attempts)
	r, _, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState:  stateRestoringTopology,
		annotationPromotedPod:         name + "-1",
		builder.AnnotationKnownMaster: promotedHost,
	}, &funcs)

	commands := wireRecordingServer(t, r)

	result := r.promotePod0AndRedirect(context.Background(), v, sts, name+"-0")

	require.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	require.Positive(t, attempts, "the record must have been attempted")

	issued := commands()
	require.Len(t, issued, 2, "the promotion and its rollback, and nothing beyond them")
	assert.Contains(t, issued[0], "REPLICAOF")
	assert.Contains(t, issued[0], "ONE", "the promotion this pass performed")
	assert.Contains(t, issued[1], "REPLICAOF")
	assert.Contains(t, issued[1], promotedHost,
		"pod-0 has to be handed back, not left as a master the annotation does not name")
}

// With nothing recorded before the promotion there is no pod to hand the role
// back to, and REPLICAOF against an empty host would be worse than the window it
// closes.
func TestPromotePod0AndRedirect_NoRollbackWithoutAPreviousMaster(t *testing.T) {
	const name = "promote-norollback"
	pod0Host := podHost(name, 0)

	attempts := 0
	funcs := rejectKnownMasterWrite(pod0Host, &attempts)
	r, _, v, sts := multiReplicaFixture(t, name, map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        name + "-1",
	}, &funcs)

	commands := wireRecordingServer(t, r)

	result := r.promotePod0AndRedirect(context.Background(), v, sts, name+"-0")

	assert.True(t, result.NeedsRequeue)
	require.Positive(t, attempts)
	assert.Len(t, commands(), 1, "only the promotion; there is nothing to roll back to")
}
