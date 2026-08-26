package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The operator restarts the pods whose TLS material it cannot hot-reload, and
// only those.
//
// The reason it has to is measured rather than assumed: a Secret volume is
// rewritten in place when cert-manager rotates the certificate, and a process
// that parsed the old bytes at startup keeps presenting them until it exits. On
// a live fleet that killed the sidecar labeler, the Sentinel cross-check and the
// ADR 0012 drain promotion on every TLS cluster whose pods outlived a rotation,
// silently, with valid material sitting in the mount.
//
// Which processes can reload, and therefore which pods are exempt:
//
//	init containers   reload    they shell out to valkey-cli per invocation
//	the sidecar       reloads   internal/tlsmaterial, added with this mechanism
//	the observer      reloads   same, and it is alone in its Deployment
//	valkey-server     unknown   never measured; treated as pinning
//	valkey-sentinel   unknown   never measured; treated as pinning
//	redis_exporter    pins      third-party, long-lived, not ours to change
//
// The restart unit is the pod, not the container, so one non-reloading process
// spends the whole pod's exemption. That is why both StatefulSets carry the
// fingerprint today and the observer Deployment carries none: a data pod holds
// valkey-server and, with metrics enabled, the exporter as well.
//
// The trigger is the rotation, not the expiry. cert-manager renews 30 days
// before expiry and the previous certificate stays valid for those 30 days, so
// the roll has a month of slack and needs no notAfter parsing, no scheduling and
// no stampede control -- the concurrency cap of ADR 0019 is the only pacing
// there is. What the slack does not cover is a roll that never starts, and that
// is what the TLSMaterialStale condition reports.

// stampTLSMaterialHash writes the fingerprint of the TLS Secret named by
// secretName onto the pod template of sts, so a rotation reaches the pods
// through the same rolling update every other pod-template change rides.
//
// It is deliberately silent and never fails the step. A Secret that is absent
// (cert-manager has not issued yet) or unreadable leaves the annotation off the
// template, and a pod without the annotation is never restarted for it -- the
// same presence rule the config and pod-spec hashes use, which is what makes the
// operator upgrade that introduces this mechanism roll nothing (ADR 0005).
func (r *ValkeyReconciler) stampTLSMaterialHash(
	ctx context.Context, v *vkov1.Valkey, sts *appsv1.StatefulSet, secretName string,
) {
	if !v.IsTLSEnabled() {
		return
	}

	hash := r.tlsMaterialHash(ctx, v, secretName)
	if hash == "" {
		return
	}

	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = map[string]string{}
	}
	sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash] = hash
}

// tlsMaterialHash reads the TLS Secret and returns its fingerprint, or the empty
// string when it cannot be read. The read is cache-served, so it costs no API
// call on a pass that already watches Secrets.
func (r *ValkeyReconciler) tlsMaterialHash(ctx context.Context, v *vkov1.Valkey, secretName string) string {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: v.Namespace}, secret)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			log.FromContext(ctx).V(1).Info("cannot read TLS secret for the material fingerprint",
				"secret", secretName, "error", err.Error())
		}
		return ""
	}
	return builder.ComputeTLSMaterialHash(secret)
}

// staleTLSPod is one pod running material the operator has already superseded.
type staleTLSPod struct {
	component string
	name      string
}

// reportTLSMaterialStale re-measures, on every pass, whether any pod still runs
// TLS material older than the Secret it mounts, and records the verdict as the
// TLSMaterialStale condition.
//
// It is a report and never a failure: the pass that finds stale pods is usually
// the pass that is rolling them, and a read it could not complete is "not
// measured", never "everything is current" -- overwriting a True on the strength
// of a failed Get would clear exactly the signal an operator is meant to act on.
func (r *ValkeyReconciler) reportTLSMaterialStale(ctx context.Context, v *vkov1.Valkey) error {
	stale, measured, err := r.scanTLSMaterial(ctx, v)
	if err != nil {
		log.FromContext(ctx).V(1).Info("could not measure TLS material staleness", "error", err.Error())
		return nil
	}
	if !measured {
		return nil
	}

	status, reason, message := metav1.ConditionFalse,
		vkov1.ReasonTLSMaterialCurrent,
		"Every pod runs the TLS material currently in its Secret"
	if len(stale) > 0 {
		status, reason, message = metav1.ConditionTrue,
			vkov1.ReasonTLSMaterialRollPending, staleTLSMessage(stale)
	}

	// Guarded on the condition already stored, the way setReconcileBlockedCondition
	// is. A level is re-measured every pass, and this one is measured on every pass
	// of every TLS cluster -- writing unconditionally would re-Get the CR through
	// setStatusCondition once per pass forever, for a verdict that changes twice per
	// rotation.
	if existing := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeTLSMaterialStale); existing != nil &&
		existing.Status == status &&
		existing.Reason == reason &&
		existing.Message == message &&
		existing.ObservedGeneration == v.Generation {
		return nil
	}

	r.setStatusCondition(ctx, v, vkov1.ConditionTypeTLSMaterialStale, status, reason, message)
	return nil
}

// staleTLSMessage renders the condition message. The pods are already in ordinal
// order per tier and the tiers are visited in a fixed order, so the message is
// stable across passes -- one that reordered itself would rewrite the status on
// every reconcile and reset the condition's LastTransitionTime with it.
func staleTLSMessage(stale []staleTLSPod) string {
	parts := make([]string, 0, len(stale))
	for _, p := range stale {
		parts = append(parts, fmt.Sprintf("%s (%s)", p.name, p.component))
	}
	return fmt.Sprintf(
		"TLS material was rotated and %s still run the previous one. They are replaced by the "+
			"rolling update; the previous certificate stays valid until it expires, which is 30 days "+
			"after the rotation at cert-manager defaults. A value that does not clear means the roll "+
			"is not happening",
		strings.Join(parts, ", "))
}

// scanTLSMaterial walks both StatefulSet tiers and returns the pods whose
// recorded fingerprint differs from the one in the Secret they mount.
//
// The second return value reports whether anything was measurable at all: no
// tier with a readable Secret and an owned StatefulSet means there is nothing to
// say, which is different from "nothing is stale".
func (r *ValkeyReconciler) scanTLSMaterial(ctx context.Context, v *vkov1.Valkey) ([]staleTLSPod, bool, error) {
	if !v.IsTLSEnabled() {
		return nil, false, nil
	}

	tiers := []struct {
		component string
		secret    string
	}{
		{common.ComponentValkey, builder.ValkeyTLSSecretName(v)},
	}
	if v.IsSentinelEnabled() {
		tiers = append(tiers, struct {
			component string
			secret    string
		}{common.ComponentSentinel, builder.SentinelTLSSecretName(v)})
	}

	var stale []staleTLSPod
	measured := false
	for _, tier := range tiers {
		tierStale, tierMeasured, err := r.scanTierTLSMaterial(ctx, v, tier.component, tier.secret)
		if err != nil {
			return nil, false, err
		}
		measured = measured || tierMeasured
		stale = append(stale, tierStale...)
	}

	return stale, measured, nil
}

// scanTierTLSMaterial compares the pods of one tier against the fingerprint of
// the Secret that tier mounts.
//
// Provenance follows ADR 0020: the StatefulSet is proven by controller
// reference, and each pod by being controlled by that StatefulSet. A foreign
// object is treated as absent and reported by nobody here -- reconcileStatefulSet
// is the one reporter for the StatefulSet, and this function is a status report,
// not a write.
func (r *ValkeyReconciler) scanTierTLSMaterial(
	ctx context.Context, v *vkov1.Valkey, component, secretName string,
) ([]staleTLSPod, bool, error) {
	sts := &appsv1.StatefulSet{}
	name := types.NamespacedName{Name: common.StatefulSetName(v, component), Namespace: v.Namespace}
	if err := r.Get(ctx, name, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !metav1.IsControlledBy(sts, v) || sts.Spec.Replicas == nil {
		return nil, false, nil
	}

	desired := r.tlsMaterialHash(ctx, v, secretName)
	if desired == "" {
		return nil, false, nil
	}

	var stale []staleTLSPod
	for i := int32(0); i < *sts.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", sts.Name, i)
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, false, err
		}
		if !podIsOurs(pod, sts) {
			continue
		}
		// A pod without the annotation predates the fingerprint and is unmeasured,
		// not stale. Reporting it would make the operator upgrade that introduces
		// this mechanism light up the whole fleet.
		if recorded := pod.Annotations[builder.AnnotationTLSMaterialHash]; recorded != "" && recorded != desired {
			stale = append(stale, staleTLSPod{component: component, name: podName})
		}
	}

	sort.Slice(stale, func(i, j int) bool { return stale[i].name < stale[j].name })
	return stale, true, nil
}
