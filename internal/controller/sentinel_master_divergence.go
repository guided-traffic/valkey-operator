package controller

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// A Sentinel that names another master than the one its tier and the data plane agree
// on is stuck there until the next failover: Sentinel adopts a new address only from a
// hello carrying a higher config epoch, and it advances its epoch before it resolves
// the new address, so a failed resolution keeps the epoch and drops the address
// (measured on wds18, 2026-11-01). Such a Sentinel neither confirms the master's
// failure nor votes on its failover, and it hands the wrong address to every client
// that asks it. See docs/adr/0040-a-sentinel-that-names-another-master-is-replaced.md.

// sentinelMasterDivergenceBound is how long a Sentinel may keep naming another master
// before the operator replaces its pod (ADR 0040 D4).
//
// It outlasts every lag of a healthy Sentinel: the failover leader names the old
// master until the replicas are reconfigured, bounded by failover-timeout
// (builder.SentinelFailoverTimeout, 60 s); a Sentinel in TILT acts on nothing for
// 30 s; a hello carrying the new address arrives every 2 s.
const sentinelMasterDivergenceBound = 2 * time.Minute

// sentinelMasterDivergenceRecheckInterval is how soon a cluster with a diverged
// Sentinel is measured again, so the bound expires on time and the condition clears
// on the first pass after the replacement.
const sentinelMasterDivergenceRecheckInterval = 30 * time.Second

// sentinelStateRecheckInterval is how often a healthy Sentinel cluster is measured at
// the latest (ADR 0040 D2). Sentinel state changes no Kubernetes object: a failover
// across a network partition, or one an administrator runs by hand, starts no pass,
// and without this the first measurement after it is the 10 h cache resync.
const sentinelStateRecheckInterval = 5 * time.Minute

// sentinelDivergenceTracker records, per CR, since when each of its Sentinel pods has
// been seen naming another master. It is in memory: an operator restart starts every
// clock again, which delays a replacement by one bound and never brings one forward.
type sentinelDivergenceTracker struct {
	mu    sync.Mutex
	since map[types.NamespacedName]map[string]time.Time
}

// observe records now for every diverged pod not tracked yet, forgets the pods of cr
// that no longer diverge, and returns since when each diverged pod has.
func (t *sentinelDivergenceTracker) observe(cr types.NamespacedName, diverged []string,
	now time.Time) map[string]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(diverged) == 0 {
		delete(t.since, cr)
		return nil
	}
	if t.since == nil {
		t.since = make(map[types.NamespacedName]map[string]time.Time)
	}
	previous := t.since[cr]
	current := make(map[string]time.Time, len(diverged))
	for _, pod := range diverged {
		if seen, ok := previous[pod]; ok {
			current[pod] = seen
		} else {
			current[pod] = now
		}
	}
	t.since[cr] = current
	return maps.Clone(current)
}

// restart sets the clock of pod to now. The operator acted on it, so the next action
// waits a whole bound again, whatever this one achieved.
func (t *sentinelDivergenceTracker) restart(cr types.NamespacedName, pod string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pods, ok := t.since[cr]; ok {
		pods[pod] = now
	}
}

// forget drops every clock of cr.
func (t *sentinelDivergenceTracker) forget(cr types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.since, cr)
}

// handleSentinelMasterDivergence compares the master each Sentinel names with the pod
// the data tier answers master from, reports the difference as SentinelMasterDiverged,
// and replaces the pod of a Sentinel that kept naming another master past
// sentinelMasterDivergenceBound.
//
// It runs on the measurement of the pass, beside recordSentinelPeerDrift, and writes
// the condition in place so it rides the status write. A pass in which no Sentinel
// answered measured nothing and writes nothing (ADR 0022 D5).
func (r *ValkeyReconciler) handleSentinelMasterDivergence(ctx context.Context, v *vkov1.Valkey,
	state *health.ClusterState) {
	requestRecheck(ctx, sentinelStateRecheckInterval)
	if len(state.SentinelMasters) == 0 {
		return
	}

	now := time.Now()
	diverged := divergedSentinels(state)
	since := r.sentinelDivergence.observe(crKey(v), diverged, now)
	recordSentinelMasterDivergence(v, state, diverged)
	if len(diverged) == 0 {
		return
	}
	requestRecheck(ctx, sentinelMasterDivergenceRecheckInterval)

	target := ""
	for _, pod := range diverged {
		if now.Sub(since[pod]) >= sentinelMasterDivergenceBound {
			target = pod
			break
		}
	}
	if target == "" {
		return
	}
	if refusal := r.divergenceReplacementRefusal(v, state, target); refusal != "" {
		log.FromContext(ctx).Info("Not replacing a Sentinel that names another master",
			"pod", target, "reason", refusal)
		return
	}
	r.replaceDivergedSentinel(ctx, v, state, target)
}

// crKey is the tracker key of a CR.
func crKey(v *vkov1.Valkey) types.NamespacedName {
	return types.NamespacedName{Namespace: v.Namespace, Name: v.Name}
}

// divergedSentinels returns the Sentinel pods that name another master than the data
// tier's, sorted so the condition message is stable across passes.
func divergedSentinels(state *health.ClusterState) []string {
	var diverged []string
	for pod, info := range state.SentinelMasters {
		if sentinelMasterAddress(info) != state.MasterAddress {
			diverged = append(diverged, pod)
		}
	}
	sort.Strings(diverged)
	return diverged
}

// sentinelMasterAddress is the master a SENTINEL MASTER reply names, in the host:port
// form of health.ClusterState.MasterAddress. With announce-hostnames the host is the
// pod FQDN, the form the operator hands to SENTINEL MONITOR itself.
func sentinelMasterAddress(info valkeyclient.SentinelMasterInfo) string {
	return fmt.Sprintf("%s:%s", info.IP, info.Port)
}

// recordSentinelMasterDivergence writes SentinelMasterDiverged from the diverged pods.
func recordSentinelMasterDivergence(v *vkov1.Valkey, state *health.ClusterState, diverged []string) {
	if len(diverged) == 0 {
		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               vkov1.ConditionTypeSentinelMasterDiverged,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "SentinelMastersAgree",
			Message: fmt.Sprintf("Every Sentinel that answered names %s, the pod the data tier answers master from",
				state.MasterPod),
		})
		return
	}

	details := make([]string, 0, len(diverged))
	for _, pod := range diverged {
		details = append(details, fmt.Sprintf("%s names %s", pod, sentinelMasterAddress(state.SentinelMasters[pod])))
	}
	meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:               vkov1.ConditionTypeSentinelMasterDiverged,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: v.Generation,
		Reason:             "SentinelNamesAnotherMaster",
		Message: fmt.Sprintf(
			"The data tier answers master from %s, but %s. A Sentinel that names another master neither "+
				"confirms that master's failure nor votes on its failover; outside a rolling update the "+
				"operator replaces the pod of one that keeps naming it for %s",
			state.MasterPod, strings.Join(details, ", "), sentinelMasterDivergenceBound),
	})
}

// divergenceReplacementRefusal returns why the measurement does not allow replacing the
// diverged Sentinel target, or "" when it does (ADR 0040 D4): exactly one data pod
// answers master, no data-tier roll is recorded, target is not leading a failover, and
// a majority of the configured Sentinels names that pod and sees it healthy. The
// majority is what makes the replacement safe: a restarted Sentinel adopts the address
// its peers announce, so it is right only if they are.
func (r *ValkeyReconciler) divergenceReplacementRefusal(v *vkov1.Valkey, state *health.ClusterState,
	target string) string {
	// observeSentinels sets SentinelPeersExpected to the configured count minus one.
	configured := state.SentinelPeersExpected + 1
	agreeing := 0
	for _, info := range state.SentinelMasters {
		if sentinelMasterAddress(info) == state.MasterAddress && info.Flags == "master" {
			agreeing++
		}
	}

	switch {
	case state.MasterCandidates != 1:
		return fmt.Sprintf("%d data pods answer master", state.MasterCandidates)
	case r.getRollingUpdateState(v) != "":
		return "a data-tier rolling update is in flight"
	case strings.Contains(state.SentinelMasters[target].Flags, "failover_in_progress"):
		// The failover leader names the old master until the replicas are reconfigured.
		return "it is leading a failover"
	case agreeing <= configured/2:
		return fmt.Sprintf("only %d of %d Sentinels name %s and see it healthy", agreeing, configured, state.MasterPod)
	}
	return ""
}

// replaceDivergedSentinel deletes the pod of a Sentinel that names another master, so
// it restarts from a fresh configuration: its init container confirms the master by
// ROLE (buildSentinelInitCommand), it boots at config epoch 0, and the first hello of
// a peer hands it the epoch and the address the majority names (ADR 0040 D5).
func (r *ValkeyReconciler) replaceDivergedSentinel(ctx context.Context, v *vkov1.Valkey,
	state *health.ClusterState, podName string) {
	logger := log.FromContext(ctx).WithValues("pod", podName)
	pod, refusal := r.divergedSentinelPod(ctx, v, podName)
	if refusal != "" {
		logger.Info("Not replacing a Sentinel that names another master", "reason", refusal)
		return
	}

	named := sentinelMasterAddress(state.SentinelMasters[podName])
	r.sentinelDivergence.restart(crKey(v), podName, time.Now())
	if err := r.deleteOwnedPod(ctx, pod); err != nil {
		logger.Error(err, "Could not delete a Sentinel pod that names another master")
		r.recordEvent(v, corev1.EventTypeWarning, "SentinelMasterDiverged",
			"Sentinel %s names %s while the data tier answers master from %s; deleting its pod failed: %v",
			podName, named, state.MasterPod, err)
		return
	}
	logger.Info("Deleted a Sentinel pod that named another master", "named", named, "master", state.MasterPod)
	r.recordEvent(v, corev1.EventTypeWarning, "SentinelMasterDiverged",
		"Sentinel %s named %s for at least %s while the data tier answers master from %s; "+
			"deleted its pod so it restarts from a fresh configuration",
		podName, named, sentinelMasterDivergenceBound, state.MasterPod)
}

// divergedSentinelPod returns the pod to delete, or why it may not be deleted. The
// gates are those of every Sentinel delete: the StatefulSet and the pod are proven
// ours (ADR 0020), no outdated pod waits for the Sentinel roll, the quorum guard holds
// with the target counted as a vote, and no pod of the tier is terminating, in the
// cached sweep and in the live look right before the delete (ADR 0026 D5).
func (r *ValkeyReconciler) divergedSentinelPod(ctx context.Context, v *vkov1.Valkey,
	podName string) (*corev1.Pod, string) {
	sts := &appsv1.StatefulSet{}
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	if err := r.Get(ctx, types.NamespacedName{Name: stsName, Namespace: v.Namespace}, sts); err != nil {
		return nil, fmt.Sprintf("reading the Sentinel StatefulSet: %v", err)
	}
	if !metav1.IsControlledBy(sts, v) || sts.Spec.Replicas == nil {
		return nil, "the Sentinel StatefulSet is not controlled by this Valkey"
	}
	scan, err := r.scanSentinelPods(ctx, v, sts)
	if err != nil {
		return nil, fmt.Sprintf("scanning the Sentinel pods: %v", err)
	}

	total := int(*sts.Spec.Replicas)
	switch {
	case scan.firstOutdatedPod != nil:
		return nil, "an outdated Sentinel pod waits for the Sentinel roll"
	case !sentinelDeleteKeepsVotes(scan.readyCount, 1, total/2+1, total):
		return nil, "deleting it would leave fewer Sentinels available than the quorum"
	case scan.terminating.name != "":
		return nil, fmt.Sprintf("Sentinel pod %s is terminating", scan.terminating.name)
	}
	if live, ok := r.liveTerminatingPod(ctx, v, common.ComponentSentinel); ok {
		return nil, fmt.Sprintf("Sentinel pod %s is terminating", live.name)
	}

	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod); err != nil {
		return nil, fmt.Sprintf("reading the pod: %v", err)
	}
	if !podIsOurs(pod, sts) {
		return nil, "the pod was not created by the Sentinel StatefulSet"
	}
	return pod, ""
}
