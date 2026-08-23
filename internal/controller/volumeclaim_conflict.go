package controller

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// A StatefulSet's volumeClaimTemplates are immutable and the operator never writes
// them, so a spec.persistence change on an existing cluster is not drift that a
// further pass converges — it needs the StatefulSet recreated. This file carries
// the refusal side of that: the sentinel error a refusing step returns, the Warning
// Events that state the supported path, and the condition that keeps the fact on
// the CR after the Events expire.
//
// The rule, the alternatives and what is deliberately left unhandled:
// docs/adr/0023-volume-claim-templates-are-immutable.md.

// Event reasons for a StatefulSet whose volumeClaimTemplates no longer match the
// spec. One reason per object family, the same rule the foreign-object reasons
// follow (ADR 0020 D5): the two conflict shapes share a reason because they share
// a cause and a remediation, and the message says which one it is.
const (
	reasonStatefulSetRecreateRequired         = "StatefulSetRecreateRequired"
	reasonSentinelStatefulSetRecreateRequired = "SentinelStatefulSetRecreateRequired"
)

// errRecreateRequired marks a step that refused to write a StatefulSet because the
// live volumeClaimTemplates cannot become the ones the spec asks for.
//
// Like errForeignObject it travels to setReconcileBlockedCondition through
// errors.Join and is recognised with errors.Is, which is why the refusal wraps this
// value instead of formatting its own message. It gets its own ReconcileBlocked
// reason for the same argument that separates a collision from a rejected write:
// an admission gate reopens by itself, this clears only when a human migrates the
// StatefulSet.
var errRecreateRequired = errors.New(
	"cannot be updated in place: its volumeClaimTemplates are immutable and no longer match spec.persistence")

// recreateRequiredError builds the error a refusing step returns. kind, name and
// the difference are repeated in the message because the joined pass error is what
// reaches the CR phase, where the step name alone says neither which object nor
// what about it is stuck.
func recreateRequiredError(kind, name, detail string) error {
	return fmt.Errorf("%s %s %w (%s)", kind, name, errRecreateRequired, detail)
}

// guardVolumeClaimTemplates compares the volumeClaimTemplates the spec asks for
// against the ones the live StatefulSet carries, reports what it finds, and returns
// an error when the difference makes the update impossible.
//
// It runs after the provenance guard, so a foreign StatefulSet is refused as
// foreign and never diagnosed as a storage conflict (ADR 0020 D1: the refusal is
// checked once, before the change detection), and before the drift check, so the
// doomed update is never submitted.
//
// The fail direction follows ADR 0020 D2, "can the CR do the job it was asked to
// do". A structural conflict fails the step: persistence.enabled=true on a
// StatefulSet that has no claims is a durability statement that is not true, and
// the write that would carry it is rejected by the API server anyway. A parameter
// conflict does not: the pod template update is legal, unrelated to the claims and
// carries the replica count with it, so holding it would wedge an atomic apply that
// changes storage and image together for a difference no write can ever settle.
func (r *ValkeyReconciler) guardVolumeClaimTemplates(ctx context.Context, v *vkov1.Valkey,
	desired, current *appsv1.StatefulSet, eventReason string) error {
	const kind = "StatefulSet"
	name := desired.Name

	conflict, detail := builder.VolumeClaimTemplatesConflict(desired, current)
	switch conflict {
	case builder.VolumeClaimsStructuralConflict:
		r.reportStorageSpecNotApplied(ctx, v, vkov1.ReasonRecreateRequired, fmt.Sprintf(
			"%s %s cannot be updated: %s. Its volumeClaimTemplates are immutable, so the "+
				"operator writes nothing to it — replica, image and label changes are held "+
				"with the storage change — until the StatefulSet is recreated.",
			kind, name, detail))
		r.warnRecreateRequired(ctx, v, eventReason, kind, name, detail)
		return recreateRequiredError(kind, name, detail)

	case builder.VolumeClaimsParameterConflict:
		r.reportStorageSpecNotApplied(ctx, v, vkov1.ReasonVolumeClaimTemplatesImmutable, fmt.Sprintf(
			"%s %s keeps the storage it was created with: %s. Its volumeClaimTemplates are "+
				"immutable and the existing PersistentVolumeClaims are reused by name, so the "+
				"requested values reach claims created later and not the ones in use. Every "+
				"other change is applied normally.",
			kind, name, detail))
		r.warnStorageParametersPinned(ctx, v, eventReason, kind, name, detail)
		return nil

	default:
		r.clearStorageSpecNotApplied(ctx, v)
		return nil
	}
}

// warnRecreateRequired reports a StatefulSet the operator will not write, and says
// what applying the change would cost.
//
// Like warnForeignObject it fires on every applicable pass rather than on a
// transition, because the conflict is a property of the cluster and not of an edge,
// and the recorder aggregates the repeats into one Event series.
//
// It deliberately does NOT spell out a recovery procedure. An earlier draft named
// the orphan-delete recovery ADR 0020 D1 documents; running it end to end for the
// first time (2026-08-23, Kind) showed that for this direction it wedges the
// statefulset-controller and, when forced past that, loses the dataset. Until that
// is fixed the Event states the cost and points at the documentation, and names the
// one action that is free: putting the spec back.
func (r *ValkeyReconciler) warnRecreateRequired(ctx context.Context, v *vkov1.Valkey,
	reason, kind, name, detail string) {
	log.FromContext(ctx).Info("StatefulSet needs recreating: its volumeClaimTemplates are immutable "+
		"and no longer match the spec; leaving it untouched", "kind", kind, "name", name, "difference", detail)
	r.recordEvent(v, corev1.EventTypeWarning, reason,
		"%s %s cannot be updated: %s. volumeClaimTemplates are immutable, so the operator writes "+
			"nothing to this StatefulSet — replica, image and label changes are held together with "+
			"the storage change. Applying the change needs the StatefulSet recreated by hand, in a "+
			"maintenance window, and the operator does not carry the dataset across that: back it up "+
			"first. The procedure and what it costs are in the spec.persistence section of the "+
			"README. Reverting spec.persistence clears this immediately and costs no downtime.",
		kind, name, detail)
}

// warnStorageParametersPinned reports storage parameters that will not reach the
// running cluster, and is careful not to promise that recreating the StatefulSet
// would change that.
//
// It would not. The claims are named data-<statefulset>-<ordinal> and are reused by
// name, so a recreated StatefulSet binds the existing ones and only a claim created
// afterwards — a scale-out — follows the new template.
func (r *ValkeyReconciler) warnStorageParametersPinned(ctx context.Context, v *vkov1.Valkey,
	reason, kind, name, detail string) {
	log.FromContext(ctx).Info("Storage parameters differ from the live StatefulSet and cannot be applied; "+
		"applying every other change", "kind", kind, "name", name, "difference", detail)
	r.recordEvent(v, corev1.EventTypeWarning, reason,
		"%s %s keeps the storage it was created with: %s. volumeClaimTemplates are immutable, so "+
			"the operator applies every other change and leaves the storage as it is. Recreating the "+
			"StatefulSet does not change it either: the PersistentVolumeClaims already exist and are "+
			"reused by name, so only claims created later follow the spec. Growing a volume is an "+
			"edit on each PersistentVolumeClaim and needs a StorageClass with "+
			"allowVolumeExpansion; changing the class means moving the data.",
		kind, name, detail)
}

// reportStorageSpecNotApplied records on the CR that the storage the spec asks for
// is not the storage that runs.
//
// The Events above are the surface a user reaches first, but they expire; this is
// the durable half, and the collector turns it into a vko_valkey_status_condition
// series for free (ADR 0021 D1). It needs no recheck cadence, unlike
// SentinelPeersStale: a claim conflict appears only when the spec changes and
// disappears only when the spec changes back or the StatefulSet is replaced, and
// both of those wake a pass on their own — a generation bump or the Owns watch on
// the StatefulSet.
func (r *ValkeyReconciler) reportStorageSpecNotApplied(ctx context.Context, v *vkov1.Valkey,
	reason, message string) {
	r.setStatusCondition(ctx, v, vkov1.ConditionTypeStorageSpecNotApplied,
		metav1.ConditionTrue, reason, message)
}

// clearStorageSpecNotApplied resolves the condition once the claims agree again.
//
// It writes only when the condition exists, so the vast majority of clusters —
// which never had a conflict — carry no condition at all rather than a permanent
// False one.
func (r *ValkeyReconciler) clearStorageSpecNotApplied(ctx context.Context, v *vkov1.Valkey) {
	if meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeStorageSpecNotApplied) == nil {
		return
	}
	r.setStatusCondition(ctx, v, vkov1.ConditionTypeStorageSpecNotApplied,
		metav1.ConditionFalse, vkov1.ReasonStorageSpecApplied,
		"The volumeClaimTemplates of the live StatefulSet match spec.persistence")
}
