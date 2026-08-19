package controller

import (
	"context"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// conditionMessageLimit caps how much of the underlying error is copied into the
// ReconcileBlocked condition message. Admission rejections quote the full webhook
// response, which can be arbitrarily long; the webhook name is always at the front.
const conditionMessageLimit = 1024

// isAdmissionRejection reports whether err is a rejection by the API server's
// admission chain rather than an ordinary write failure.
//
// Two shapes matter, both observed in the 2026-08-19 infra-d incident and in
// normal policy-engine operation:
//
//   - the webhook could not be called at all and its failurePolicy is Fail —
//     "Internal error occurred: failed calling webhook \"mutate.kyverno.svc-fail\":
//     no endpoints available for service \"kyverno-svc\"";
//   - the webhook answered and denied the request —
//     "admission webhook \"...\" denied the request: ...".
//
// The message is matched instead of only the typed reason because callers wrap
// these errors (fmt.Errorf("sentinel statefulset: %w", err)), and because the
// denial shape is reported as Forbidden, not as an internal error.
func isAdmissionRejection(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "failed calling webhook"):
		return true
	case strings.Contains(msg, "admission webhook") && strings.Contains(msg, "denied the request"):
		return true
	default:
		// Anything else the API server reports from the admission chain as an
		// internal error (e.g. a failing built-in admission plugin).
		return apierrors.IsInternalError(err) && strings.Contains(msg, "admission")
	}
}

// reconcileBlockedReason maps a reconcile error to the ReconcileBlocked reason.
func reconcileBlockedReason(err error) string {
	if isAdmissionRejection(err) {
		return vkov1.ReasonAdmissionWebhookDenied
	}
	return vkov1.ReasonWriteFailed
}

// truncateConditionMessage shortens a message to conditionMessageLimit runes.
func truncateConditionMessage(msg string) string {
	runes := []rune(msg)
	if len(runes) <= conditionMessageLimit {
		return msg
	}
	return string(runes[:conditionMessageLimit]) + "..."
}

// setReconcileBlockedCondition sets ReconcileBlocked from the outcome of a
// reconcileResources pass: True with the failing write's error when err != nil,
// False once a pass completed.
//
// Writes are skipped when nothing would change — a healthy cluster reconciles
// every few seconds and must not produce a status write per pass, and a
// persistently blocked one must not rewrite an identical condition either.
func (r *ValkeyReconciler) setReconcileBlockedCondition(ctx context.Context, v *vkov1.Valkey, err error) {
	existing := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeReconcileBlocked)

	if err == nil {
		// Never set before, or already cleared: nothing to do.
		if existing == nil || existing.Status == metav1.ConditionFalse {
			return
		}
		r.setStatusCondition(ctx, v,
			vkov1.ConditionTypeReconcileBlocked,
			metav1.ConditionFalse,
			vkov1.ReasonReconcileSucceeded,
			"All managed resources reconciled successfully")
		return
	}

	reason := reconcileBlockedReason(err)
	message := truncateConditionMessage(err.Error())
	if existing != nil &&
		existing.Status == metav1.ConditionTrue &&
		existing.Reason == reason &&
		existing.Message == message {
		return
	}
	r.setStatusCondition(ctx, v,
		vkov1.ConditionTypeReconcileBlocked,
		metav1.ConditionTrue,
		reason,
		message)
}
