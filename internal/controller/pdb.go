package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// reconcilePodDisruptionBudgets reconciles the data and Sentinel PodDisruptionBudgets.
//
// Why the operator manages them at all: a node drain evicts every pod on the node
// at once. Without a budget that can be all data pods (the 2026-08-19 infra-d
// incident) or the Sentinel majority, which removes automatic failover exactly
// when it is needed. The Eviction API is the only thing that serializes voluntary
// disruptions, and it needs a PDB to do so.
//
// Both budgets are opt-in (spec.podDisruptionBudget.enabled) because a PDB the
// operator creates alongside a user-managed one would make eviction fail outright:
// the Eviction API refuses a pod covered by more than one budget.
func (r *ValkeyReconciler) reconcilePodDisruptionBudgets(ctx context.Context, valkey *vkov1.Valkey) error {
	return r.runReconcileSteps(ctx, valkey, []reconcileStep{
		{name: "data PodDisruptionBudget", run: r.reconcileDataPodDisruptionBudget},
		{name: "Sentinel PodDisruptionBudget", run: r.reconcileSentinelPodDisruptionBudget},
	})
}

// reconcileDataPodDisruptionBudget creates the data PDB when it applies and removes
// it otherwise (disabled, or the StatefulSet scaled below the minimum replica count).
func (r *ValkeyReconciler) reconcileDataPodDisruptionBudget(ctx context.Context, v *vkov1.Valkey) error {
	if !v.NeedsDataPodDisruptionBudget() {
		if v.IsPodDisruptionBudgetEnabled() && v.Spec.Replicas < vkov1.MinPDBReplicas {
			logPodDisruptionBudgetSkip(ctx, builder.PodDisruptionBudgetName(v), v.Spec.Replicas)
		}
		return r.cleanupPodDisruptionBudget(ctx, v, builder.PodDisruptionBudgetName(v))
	}

	if err := r.reconcilePodDisruptionBudget(ctx, v, builder.BuildValkeyPodDisruptionBudget(v)); err != nil {
		return err
	}
	r.warnIfDataBudgetProtectsNothing(ctx, v)
	return nil
}

// reasonPodDisruptionBudgetTooPermissive is the Event reason for a data budget
// that permits evicting every data pod at once.
const reasonPodDisruptionBudgetTooPermissive = "PodDisruptionBudgetTooPermissive"

// warnIfDataBudgetProtectsNothing warns while maxUnavailable is not smaller than
// spec.replicas — the budget then permits a single drain to take every data pod.
//
// It runs on every pass in which the budget applies, not only when the PDB was
// written. Scaling spec.replicas down into the condition (5 -> 2 with
// maxUnavailable 2) leaves the PDB object byte-identical, so a write-gated warning
// stayed silent for exactly the change that created the hazard. The repetition
// that costs is the Event, and the recorder aggregates a repeated Event into one
// series instead of new objects.
func (r *ValkeyReconciler) warnIfDataBudgetProtectsNothing(ctx context.Context, v *vkov1.Valkey) {
	maxUnavailable := v.PodDisruptionBudgetMaxUnavailable()
	if maxUnavailable < v.Spec.Replicas {
		return
	}

	name := builder.PodDisruptionBudgetName(v)
	log.FromContext(ctx).Info("PodDisruptionBudget allows every data pod to be evicted at once; "+
		"spec.podDisruptionBudget.maxUnavailable is not smaller than spec.replicas",
		"name", name, "maxUnavailable", maxUnavailable, "replicas", v.Spec.Replicas)
	r.recordEvent(v, corev1.EventTypeWarning, reasonPodDisruptionBudgetTooPermissive,
		"PodDisruptionBudget %s allows every data pod to be evicted at once "+
			"(maxUnavailable %d is not smaller than replicas %d)",
		name, maxUnavailable, v.Spec.Replicas)
}

// reconcileSentinelPodDisruptionBudget creates the quorum-preserving Sentinel PDB
// when it applies and removes it otherwise (PDBs or Sentinel disabled, or fewer
// than two Sentinel replicas).
func (r *ValkeyReconciler) reconcileSentinelPodDisruptionBudget(ctx context.Context, v *vkov1.Valkey) error {
	if !v.NeedsSentinelPodDisruptionBudget() {
		if v.IsPodDisruptionBudgetEnabled() && v.IsSentinelEnabled() &&
			v.Spec.Sentinel.Replicas < vkov1.MinPDBReplicas {
			logPodDisruptionBudgetSkip(ctx, builder.SentinelPodDisruptionBudgetName(v), v.Spec.Sentinel.Replicas)
		}
		return r.cleanupPodDisruptionBudget(ctx, v, builder.SentinelPodDisruptionBudgetName(v))
	}

	if err := r.reconcilePodDisruptionBudget(ctx, v, builder.BuildSentinelPodDisruptionBudget(v)); err != nil {
		return err
	}
	r.warnIfSentinelBudgetBlocksEveryDrain(ctx, v)
	return nil
}

// reasonSentinelPodDisruptionBudgetBlocksDrains is the Event reason for a Sentinel
// budget whose quorum equals the replica count, so no voluntary disruption is
// permitted at all.
const reasonSentinelPodDisruptionBudgetBlocksDrains = "SentinelPodDisruptionBudgetBlocksDrains"

// warnIfSentinelBudgetBlocksEveryDrain warns while the Sentinel quorum equals
// spec.sentinel.replicas — with an even Sentinel count of 2 that is the case, and
// the Eviction API then refuses every eviction indefinitely: a node drain hosting
// a Sentinel pod never completes without manual intervention.
//
// The formula stays: minAvailable below the quorum would let a drain take the
// Sentinel majority and thereby automatic failover. The gap NA7 closes is that the
// consequence was documented in a builder comment only and invisible at runtime.
//
// Like the data-side warning, it runs on every pass in which the budget applies
// rather than on writes. Scaling spec.sentinel.replicas 3 -> 2 leaves the quorum
// at 2, so the PDB object is byte-identical and a write-gated warning would stay
// silent for exactly the change that turned the budget into a drain blocker.
func (r *ValkeyReconciler) warnIfSentinelBudgetBlocksEveryDrain(ctx context.Context, v *vkov1.Valkey) {
	replicas := v.Spec.Sentinel.Replicas
	minAvailable := builder.SentinelQuorumFor(replicas)
	if minAvailable < replicas {
		return
	}

	name := builder.SentinelPodDisruptionBudgetName(v)
	log.FromContext(ctx).Info("Sentinel PodDisruptionBudget blocks every voluntary disruption; "+
		"the quorum equals spec.sentinel.replicas, so node drains hosting a Sentinel pod stall. "+
		"Use an odd Sentinel count of 3 or more",
		"name", name, "minAvailable", minAvailable, "replicas", replicas)
	r.recordEvent(v, corev1.EventTypeWarning, reasonSentinelPodDisruptionBudgetBlocksDrains,
		"PodDisruptionBudget %s blocks every voluntary disruption "+
			"(minAvailable %d equals spec.sentinel.replicas %d); node drains hosting a Sentinel pod "+
			"will not complete. Use an odd Sentinel count of 3 or more",
		name, minAvailable, replicas)
}

// logPodDisruptionBudgetSkip records why an enabled PDB was not created. A
// StatefulSet with a single pod is deliberately left uncovered: maxUnavailable=1
// would permit evicting the only pod and minAvailable=1 would block node drains
// forever, neither of which makes a non-HA instance safer.
func logPodDisruptionBudgetSkip(ctx context.Context, name string, replicas int32) {
	log.FromContext(ctx).V(1).Info("Skipping PodDisruptionBudget: fewer replicas than the minimum",
		"name", name, "replicas", replicas, "minimum", vkov1.MinPDBReplicas)
}

// reconcilePodDisruptionBudget ensures a single PDB matches the desired state.
func (r *ValkeyReconciler) reconcilePodDisruptionBudget(ctx context.Context, v *vkov1.Valkey,
	desired *policyv1.PodDisruptionBudget) error {
	logger := log.FromContext(ctx)
	builder.ApplyOperatorVersion(desired, r.OperatorVersion)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on PodDisruptionBudget %s: %w", desired.Name, err)
	}

	current := &policyv1.PodDisruptionBudget{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating PodDisruptionBudget", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if builder.PodDisruptionBudgetHasChanged(desired, current) ||
		builder.OperatorVersionChanged(current, r.OperatorVersion) {
		logger.Info("Updating PodDisruptionBudget", "name", desired.Name)
		builder.ApplyPodDisruptionBudgetSpec(desired, current)
		current.Labels = desired.Labels
		builder.ApplyOperatorVersion(current, r.OperatorVersion)
		return r.Update(ctx, current)
	}

	return nil
}

// cleanupPodDisruptionBudget deletes the named PDB if it exists.
func (r *ValkeyReconciler) cleanupPodDisruptionBudget(ctx context.Context, v *vkov1.Valkey, name string) error {
	logger := log.FromContext(ctx)
	pdb := &policyv1.PodDisruptionBudget{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: v.Namespace}, pdb); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	logger.Info("Deleting PodDisruptionBudget", "name", name)
	if err := r.Delete(ctx, pdb); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting poddisruptionbudget: %w", err)
	}
	return nil
}
