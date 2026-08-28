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
//
// Two things the mechanism does not cover, both recorded rather than glossed:
//
//   - A pod created before this operator version carries no record and is
//     unmeasured, never stale. That is the whole of the upgrade-neutrality story
//     (ADR 0005). It is only the legacy population: the operator never persists
//     a TLS pod template without a record (ADR 0030 D12), so every pod it
//     creates from here on is measurable from birth.
//   - The fingerprint answers "did the roll happen" and never "is the material
//     unchanged": whoever can write the Secret can keep the digest identical
//     (ADR 0030 D11).

// ensureTLSMaterialRecord stamps the fingerprint of the TLS Secret named by
// secretName onto the pod template of desired and reports whether that template
// may be persisted. The invariant it enforces is ADR 0030 D12: the operator
// never persists a TLS pod template without a material record, because every pod
// built from a record-less template is permanently exempt from rotation rolls --
// the presence rule cannot tell it from a pre-mechanism pod (T27).
//
// The record goes into the env of carrierContainer -- the sidecar on the data
// tier, the sentinel container on the Sentinel tier -- and not into the template
// annotations, because a pod can patch its own metadata and cannot patch its own
// spec (ADR 0031).
//
// Three cases, over what is known rather than over lifecycle phase:
//
//  1. The Secret is readable: its fingerprint is stamped. The only case that
//     existed before D12, byte-identical to it.
//  2. The Secret is unreadable and the persisted template carries a record: that
//     record is stamped onto desired. An unreadable Secret never erases a
//     record -- without this, one blind pass strips the fingerprint off the live
//     template and every pod recreated in that window comes back unmeasurable
//     (T24(c)). current is read only after reconcileStatefulSet proved
//     ownership, because provenance of the object is not provenance of the
//     field (ADR 0020 D10); on the create path there is no current.
//  3. Neither is known: the write is refused. False, not an error -- the pass
//     goes on and asks to be re-entered, and the Secret watch re-triggers the
//     moment cert-manager issues, so on the ordinary create path the
//     StatefulSet appears seconds after the CR. A Secret that never appears
//     leaves the tier unprovisioned (create) or its template unwritten
//     (update), which is the state TLS itself would be in: a pod from that
//     template could not mount its material either.
func (r *ValkeyReconciler) ensureTLSMaterialRecord(
	ctx context.Context, v *vkov1.Valkey, desired, current *appsv1.StatefulSet,
	secretName, carrierContainer string,
) bool {
	if !v.IsTLSEnabled() {
		return true
	}

	hash := r.tlsMaterialHash(ctx, v, secretName)
	if hash == "" && current != nil {
		hash = tlsMaterialHashFromSts(current)
	}
	if hash == "" {
		log.FromContext(ctx).Info(
			"refusing to persist a TLS pod template without a material record; waiting for the Secret",
			"statefulset", desired.Name, "secret", secretName)
		requestRecheck(ctx, foreignObjectRecheckInterval)
		return false
	}

	builder.StampTLSMaterialHash(desired, carrierContainer, hash)
	return true
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

// staleTLSPod is one pod running material the operator has already superseded,
// or one it cannot measure at all -- which list it is on says which.
type staleTLSPod struct {
	component string
	name      string
}

// tlsMaterialScan is the verdict of one walk over both StatefulSet tiers.
//
// stale and unmeasured are disjoint: a pod is stale when its recorded
// fingerprint differs from the Secret it mounts, and unmeasured when it records
// nothing at all -- the legacy population from before the mechanism, which a
// rotation will never replace (ADR 0005, ADR 0030 D8). measured reports whether
// every tier could be inspected; it gates the all-clear, never a finding.
type tlsMaterialScan struct {
	stale      []staleTLSPod
	unmeasured []staleTLSPod
	measured   bool
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
	if !v.IsTLSEnabled() {
		r.clearTLSMaterialStaleOnDisable(ctx, v)
		return nil
	}

	scan, err := r.scanTLSMaterial(ctx, v)
	if err != nil {
		log.FromContext(ctx).V(1).Info("could not measure TLS material staleness", "error", err.Error())
		return nil
	}
	// A tier that could not be measured suppresses the all-clear, never a
	// finding: stale pods in the tier that was readable are reported either way,
	// but "every pod is current" is only written when every tier was actually
	// inspected. Absorbing an unreadable tier into the affirmative sentence is
	// T24(a), and it contradicted this function's own stated rule.
	if len(scan.stale) == 0 && !scan.measured {
		return nil
	}

	// Reason precedence is fixed: a stale pod outranks an unmeasured one, and
	// both outrank the all-clear. The identical-write guard below compares the
	// reason, so a precedence that flapped would rewrite the status every pass.
	status, reason, message := metav1.ConditionFalse,
		vkov1.ReasonTLSMaterialCurrent,
		"Every pod runs the TLS material currently in its Secret"
	switch {
	case len(scan.stale) > 0:
		status, reason, message = metav1.ConditionTrue,
			vkov1.ReasonTLSMaterialRollPending, staleTLSMessage(scan.stale)
	case len(scan.unmeasured) > 0:
		// T24(b): the record-less legacy population used to be absorbed into the
		// affirmative sentence, on the one tier no operator upgrade ever rolls.
		// Status stays False -- unmeasured is not stale, and the shipped alert
		// matches status="True" only -- but the reason and the names stop the CR
		// claiming a coverage it does not have.
		reason, message = vkov1.ReasonTLSMaterialUnmeasured, unmeasuredTLSMessage(scan.unmeasured)
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

// clearTLSMaterialStaleOnDisable retracts a standing True on a cluster that has
// turned TLS off. Without it the condition has no writer once TLS is disabled,
// so a True carried into the switch stays True for the life of the CR and the
// shipped ValkeyTLSMaterialStale alert fires on it indefinitely (T24(d)).
//
// It is presence-guarded in both directions: a cluster that never carried the
// condition never gains one here (ADR 0005), and a standing False is left
// untouched -- it alerts nobody, and rewriting its reason would reset a
// LastTransitionTime for a cluster nothing changed on.
func (r *ValkeyReconciler) clearTLSMaterialStaleOnDisable(ctx context.Context, v *vkov1.Valkey) {
	existing := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeTLSMaterialStale)
	if existing == nil || existing.Status != metav1.ConditionTrue {
		return
	}
	r.setStatusCondition(ctx, v, vkov1.ConditionTypeTLSMaterialStale, metav1.ConditionFalse,
		vkov1.ReasonTLSMaterialNotApplicable, "TLS is disabled; there is no material left to be stale")
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

// unmeasuredTLSMessage renders the T24(b) verdict: no measured pod is stale, but
// the named pods record no fingerprint and a rotation will not replace them.
// Same ordering contract as staleTLSMessage.
func unmeasuredTLSMessage(unmeasured []staleTLSPod) string {
	parts := make([]string, 0, len(unmeasured))
	for _, p := range unmeasured {
		parts = append(parts, fmt.Sprintf("%s (%s)", p.name, p.component))
	}
	return fmt.Sprintf(
		"No measured pod runs superseded TLS material, but %s predate the material fingerprint "+
			"and cannot be measured: a certificate rotation will not replace them. They arm "+
			"themselves the next time they are replaced for any other reason; to close the gap now, "+
			"delete them one at a time or make any pod-template change",
		strings.Join(parts, ", "))
}

// scanTLSMaterial walks both StatefulSet tiers and returns the pods whose
// recorded fingerprint differs from the one in the Secret they mount, the pods
// that record nothing and cannot be measured at all, and whether every tier was
// measurable.
//
// measured is an AND across the tiers, not an OR: one readable tier says nothing
// about the other, and the caller writes the all-clear only over a complete
// measurement (T24(a)).
func (r *ValkeyReconciler) scanTLSMaterial(ctx context.Context, v *vkov1.Valkey) (tlsMaterialScan, error) {
	if !v.IsTLSEnabled() {
		return tlsMaterialScan{}, nil
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

	scan := tlsMaterialScan{measured: true}
	for _, tier := range tiers {
		tierScan, err := r.scanTierTLSMaterial(ctx, v, tier.component, tier.secret)
		if err != nil {
			return tlsMaterialScan{}, err
		}
		scan.measured = scan.measured && tierScan.measured
		scan.stale = append(scan.stale, tierScan.stale...)
		scan.unmeasured = append(scan.unmeasured, tierScan.unmeasured...)
	}

	return scan, nil
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
) (tlsMaterialScan, error) {
	sts := &appsv1.StatefulSet{}
	name := types.NamespacedName{Name: common.StatefulSetName(v, component), Namespace: v.Namespace}
	if err := r.Get(ctx, name, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return tlsMaterialScan{}, nil
		}
		return tlsMaterialScan{}, err
	}
	if !metav1.IsControlledBy(sts, v) || sts.Spec.Replicas == nil {
		return tlsMaterialScan{}, nil
	}

	desired := r.tlsMaterialHash(ctx, v, secretName)
	if desired == "" {
		return tlsMaterialScan{}, nil
	}

	scan := tlsMaterialScan{measured: true}
	for i := int32(0); i < *sts.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", sts.Name, i)
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: v.Namespace}, pod); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return tlsMaterialScan{}, err
		}
		if !podIsOurs(pod, sts) {
			continue
		}
		// A pod carrying no record predates the fingerprint and is unmeasured, not
		// stale -- reporting it as stale would make the operator upgrade that
		// introduces this mechanism light up the whole fleet. Since D12 the
		// operator writes no record-less pod, so this is the legacy population,
		// and it is *named* rather than silently absorbed into the all-clear
		// (T24(b)): a rotation will never replace these pods, and the tier that
		// matters is the Sentinel one, which no operator upgrade rolls either.
		switch recorded := builder.RecordedTLSMaterialHash(&pod.Spec, pod.Annotations); {
		case recorded == "":
			scan.unmeasured = append(scan.unmeasured, staleTLSPod{component: component, name: podName})
		case recorded != desired:
			scan.stale = append(scan.stale, staleTLSPod{component: component, name: podName})
		}
	}

	sort.Slice(scan.stale, func(i, j int) bool { return scan.stale[i].name < scan.stale[j].name })
	sort.Slice(scan.unmeasured, func(i, j int) bool { return scan.unmeasured[i].name < scan.unmeasured[j].name })
	return scan, nil
}
