package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// Every object this operator manages is named from the CR name, so a generated
// name can be held by an object the operator never created. This file carries the
// refusal side of that: the sentinel error a refusing step returns, the Warning
// Events that name the colliding object, and the recheck cadence that lets a pass
// which refused without failing still look again.
//
// The rule and its alternatives: docs/adr/0020-write-only-what-the-operator-owns.md.
// The deletion half predates it: docs/adr/0006-delete-only-what-the-operator-owns.md.

// Event reasons for a generated name held by an object this Valkey does not control.
// One reason per object family, so a user filtering Events sees which name collided
// without parsing the message.
const (
	reasonObserverServiceAccountNotOwned = "ObserverServiceAccountNotOwned"
	reasonSidecarServiceAccountNotOwned  = "SidecarServiceAccountNotOwned"
	reasonSidecarRoleNotOwned            = "SidecarRoleNotOwned"
	reasonSidecarRoleBindingNotOwned     = "SidecarRoleBindingNotOwned"
	reasonStatefulSetNotOwned            = "StatefulSetNotOwned"
	reasonSentinelStatefulSetNotOwned    = "SentinelStatefulSetNotOwned"
	reasonObserverDeploymentNotOwned     = "ObserverDeploymentNotOwned"
	reasonServiceNotOwned                = "ServiceNotOwned"
	reasonConfigMapNotOwned              = "ConfigMapNotOwned"
	reasonNetworkPolicyNotOwned          = "NetworkPolicyNotOwned"
	reasonServiceMonitorNotOwned         = "ServiceMonitorNotOwned"
	reasonCertificateNotOwned            = "CertificateNotOwned"
	reasonPodNotOwned                    = "PodNotOwned"
)

// foreignObjectRecheckInterval is how soon a pass that refused a write without
// failing looks again.
//
// It matches reconcileRetryMaxDelay, the cap on the rate limiter that drives the
// refusals which *do* fail their step, so both kinds of refusal recover at the same
// cadence. The interval is load-bearing rather than cosmetic: a colliding object
// carries no ownerReference to this CR, so the Owns(&corev1.ServiceAccount{}) watch
// in SetupWithManager never fires for it, and GenerationChangedPredicate drops the
// operator's own status writes — nothing else re-enters Reconcile after an
// administrator removes the collision.
const foreignObjectRecheckInterval = 30 * time.Second

// errForeignObject marks a step that refused to write because a generated name is
// held by an object this Valkey does not control.
//
// It travels to setReconcileBlockedCondition through errors.Join and is recognised
// with errors.Is, which is why every refusal wraps this value instead of formatting
// its own message. The distinct ReconcileBlocked reason matters because the two
// causes need opposite responses: a rejected write clears itself when the admission
// gate reopens, a collision clears only when a human acts.
var errForeignObject = errors.New("held by an object this Valkey does not control")

// foreignObjectError builds the error a refusing step returns. kind and name are
// repeated in the message because the joined pass error is what reaches the CR
// phase, where the step name alone does not say which object collided.
func foreignObjectError(kind, name string) error {
	return fmt.Errorf("%s %s is %w", kind, name, errForeignObject)
}

// passState carries facts a reconcile step discovers that only the pass as a whole
// can act on. It is created per pass in Reconcile and reached through the context,
// so it stays per-CR at MaxConcurrentReconciles > 1 the same way the blocked-pass
// marker does (docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md,
// D3).
//
// The steps of a pass run sequentially in runReconcileSteps, so the mutex guards
// nothing today. It is here because ADR 0019 D3 is a standing constraint and not a
// one-time audit: a future step that fans out would otherwise race silently.
type passState struct {
	mu      sync.Mutex
	recheck time.Duration
}

// requestRecheck records that this pass wants to be re-entered after d even though
// it is ending without an error. The shortest request wins.
func (p *passState) requestRecheck(d time.Duration) {
	if d <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.recheck <= 0 || d < p.recheck {
		p.recheck = d
	}
}

// interval returns the recheck this pass asked for, or zero.
func (p *passState) interval() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.recheck
}

// passStateKey addresses the per-pass state on the context.
type passStateKey struct{}

// withPassState attaches a fresh per-pass state to ctx. Reconcile does this once
// per pass, before the first step runs.
func withPassState(ctx context.Context, state *passState) context.Context {
	return context.WithValue(ctx, passStateKey{}, state)
}

// requestRecheck asks the pass carrying ctx to be re-entered after d. It is a no-op
// when ctx carries no pass state, which is the case in the unit tests that call a
// single reconcile step directly.
func requestRecheck(ctx context.Context, d time.Duration) {
	if state, ok := ctx.Value(passStateKey{}).(*passState); ok && state != nil {
		state.requestRecheck(d)
	}
}

// applyRecheck folds a pass's recheck request into the result it is about to
// return, without lengthening a requeue the pass already asked for.
//
// It is applied only on the error-free path. controller-runtime drives the retry
// from the returned error otherwise, and setting both would make it log the result
// as ignored.
func applyRecheck(result ctrl.Result, recheck time.Duration) ctrl.Result {
	if recheck <= 0 {
		return result
	}
	if result.RequeueAfter <= 0 || recheck < result.RequeueAfter {
		result.RequeueAfter = recheck
	}
	return result
}

// warnForeignObject reports a generated name held by an object this Valkey does not
// control, and states what the operator did not do to it.
//
// Modelled on warnPodDisruptionBudgetNotOwned: it fires on every applicable pass
// rather than on a transition, because the collision is a property of the cluster
// and not of an edge, and the recorder aggregates the repeats into one Event series.
// Unlike the PDB warning it needs no opt-in gate. That gate exists because a
// hand-written PodDisruptionBudget under the StatefulSet name was the documented
// workaround before the feature existed, so the warning had a large legitimate
// population to stay quiet for (docs/adr/0004-opt-in-poddisruptionbudgets.md, D11).
// Nothing hand-writes a <cr-name>-sidecar ServiceAccount as a workaround, so the
// population here is actual collisions and every one of them is worth reporting.
func (r *ValkeyReconciler) warnForeignObject(ctx context.Context, v *vkov1.Valkey,
	reason, kind, name, consequence string) {
	log.FromContext(ctx).Info("Managed name is held by an object this Valkey does not control; "+
		"leaving it untouched", "kind", kind, "name", name, "consequence", consequence)
	r.recordEvent(v, corev1.EventTypeWarning, reason,
		"%s %s exists but is not owned by this Valkey; leaving it untouched. %s",
		kind, name, consequence)
}

// deleteIfOwned deletes obj, but only when this Valkey controls it, and keeps the
// delete on the object the ownership decision was made on.
//
// Both halves are docs/adr/0006-delete-only-what-the-operator-owns.md: the provenance
// proof (D2), because a generated name is input the CR author chooses and not evidence
// of ownership, and the UID precondition (D8, D9), because the decision is made on a
// cache-backed read and the name can hold a different object by the time the Delete
// lands. A Conflict therefore means the object that decision was about is already
// gone: the guard did its job and the pass is not failed over it.
//
// The caller has already read obj, so the guard costs no extra API call. kind appears
// in the log lines and in the error because the name alone does not say which of the
// managed families collided.
func (r *ValkeyReconciler) deleteIfOwned(ctx context.Context, v *vkov1.Valkey, obj client.Object, kind string) error {
	logger := log.FromContext(ctx)

	if !metav1.IsControlledBy(obj, v) {
		logger.Info("Skipping deletion: the name is held by an object this Valkey does not control",
			"kind", kind, "name", obj.GetName())
		return nil
	}

	logger.Info("Deleting object", "kind", kind, "name", obj.GetName())
	uid := obj.GetUID()
	switch err := r.Delete(ctx, obj, client.Preconditions{UID: &uid}); {
	case err == nil || apierrors.IsNotFound(err):
		return nil
	case apierrors.IsConflict(err):
		logger.Info("Skipping deletion: the object was replaced under its name",
			"kind", kind, "name", obj.GetName())
		return nil
	default:
		return fmt.Errorf("deleting %s %s: %w", kind, obj.GetName(), err)
	}
}

// --- pods: the two-hop half of D1 ---
//
// A pod is the only managed object whose controller is not the CR. The
// statefulset-controller creates it and stamps itself, so the proof runs
// pod -> StatefulSet -> CR, and every link compares a UID rather than a name.
// docs/adr/0020-write-only-what-the-operator-owns.md D9.

// podIsOurs reports whether sts created pod.
//
// The caller supplies a StatefulSet it has already proven belongs to this CR —
// `reconcileStatefulSet` refuses the pass otherwise (D1), and the rolling-update
// entry points re-check it — which is what makes the second hop free here.
//
// The bound, stated because this guard is easy to overstate: the
// statefulset-controller adopts orphan pods matching its selector and stamps its own
// controller reference on them, so a pod built to carry this cluster's label set and
// left without a controller becomes genuinely ours by upstream rules. What this
// closes is a collision and a stray, not a deliberate mimic. That adoption behaviour
// is read from the API contract and is not reproduced anywhere in this repo.
func podIsOurs(pod *corev1.Pod, sts *appsv1.StatefulSet) bool {
	return pod != nil && sts != nil && metav1.IsControlledBy(pod, sts)
}

// ownedDataStatefulSet returns the data StatefulSet when this Valkey controls it,
// and nil when it is absent or foreign. Pod paths that do not already
// hold the object use it to get the second hop of podIsOurs; the read is
// cache-served, so it costs no API call.
//
// A nil result is not an error: every caller here treats "no StatefulSet we own"
// the same way it treats "no pods", and `reconcileStatefulSet` is the one reporter
// for the StatefulSet itself (D8).
func (r *ValkeyReconciler) ownedDataStatefulSet(
	ctx context.Context, v *vkov1.Valkey,
) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{}
	name := types.NamespacedName{
		Name:      common.StatefulSetName(v, common.ComponentValkey),
		Namespace: v.Namespace,
	}
	if err := r.Get(ctx, name, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if !metav1.IsControlledBy(sts, v) {
		return nil, nil
	}
	return sts, nil
}

// filterOwnedPods splits pods into the ones sts created and the names of the rest.
//
// The refused names are returned rather than logged here because only one caller
// reports them: `reconcileSidecarRole` runs on every pass and lists the data pods
// anyway, so it carries the Event for the whole family and every other filtering
// path stays quiet — the same one-reporter rule D8 sets for the StatefulSet.
func filterOwnedPods(pods []corev1.Pod, sts *appsv1.StatefulSet) ([]corev1.Pod, []string) {
	owned := make([]corev1.Pod, 0, len(pods))
	var refused []string
	for i := range pods {
		if podIsOurs(&pods[i], sts) {
			owned = append(owned, pods[i])
			continue
		}
		refused = append(refused, pods[i].Name)
	}
	return owned, refused
}

// podUnderNameIsOurs reads the pod holding a generated name and reports whether sts
// created it. A pod that is absent or unreadable reports false, which every caller
// already treats the same way it treats an unreachable pod.
//
// The read is cache-served: the Pod informer exists because the rolling update Gets
// pods on every pass.
func (r *ValkeyReconciler) podUnderNameIsOurs(
	ctx context.Context, v *vkov1.Valkey, podName string, sts *appsv1.StatefulSet,
) bool {
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod); err != nil {
		return false
	}
	return podIsOurs(pod, sts)
}

// deleteOwnedPod deletes a pod the caller has already proven this cluster's
// StatefulSet created, and keeps the delete on that very object.
//
// The UID precondition is the same one every other delete in this operator sends
// (docs/adr/0006-delete-only-what-the-operator-owns.md, D8): the provenance decision
// is made on a cache-backed read, so the name can hold a different pod by the time
// the Delete lands — and here the rolling update deletes pods it intends to see
// recreated, which is exactly the window in which a name changes hands.
//
// NotFound and Conflict both report success, because both mean the pod that decision
// was about is already gone. Every caller answers success with a requeue, so the next
// pass re-reads rather than acting on a stale verdict.
func (r *ValkeyReconciler) deleteOwnedPod(ctx context.Context, pod *corev1.Pod) error {
	uid := pod.UID
	switch err := r.Delete(ctx, pod, client.Preconditions{UID: &uid}); {
	case err == nil || apierrors.IsNotFound(err) || apierrors.IsConflict(err):
		return nil
	default:
		return err
	}
}
