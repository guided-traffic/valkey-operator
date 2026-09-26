package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// The operator side of docs/adr/0032-generated-pods-run-rootless.md: the builder
// renders only the rootless posture, and this file decides the two things that
// depend on what an earlier operator left running -- whether the data template
// carries the ownership repair (D2), and which single pod keeps running as root on
// a deferred update (D3). Both are derived from the pods on every pass and stored
// nowhere.

// dataOwnershipRepairNeeded is the migration evidence of ADR 0032 D2.
//
// The repair is added when the live StatefulSet or a data pod proven ours still has
// the shape an earlier operator built -- no runAsNonRoot -- because the data on the
// volumes was written the same way, as uid 0. The template counts as evidence on its
// own: at the first pass after the upgrade a pod may be missing (evicted, killed,
// its creation blocked), and the rootless template would otherwise be written
// without the repair, recreating that pod onto its root-owned volume. Pod specs are
// immutable, so no label or annotation can forge the pod evidence, and both kinds
// survive an operator restart.
//
// Once carried, it is kept until every ordinal of the tier holds a migrated pod:
// proven ours, rootless, and past the repair -- its pre-flight exited 0, or it has
// been Ready. A missing pod, or a rootless one that never got that far (an image it
// cannot pull, a node it cannot schedule on), does not count. The asymmetry closes
// two races the plain "any legacy pod exists" rule has on the last migration of a
// tier: a pass between the last legacy pod disappearing and its recreation, and a
// replacement that is rootless but has not run its repair yet.
//
// Persistence is read off the live StatefulSet, never off the CR: a persistence
// toggle the operator refused to apply (ADR 0023) must not change what it does to
// the pods that exist. The range is the live StatefulSet's ordinal range, never a
// label selector (ADR 0026 D7). A pod this StatefulSet did not create is treated as
// absent (ADR 0020): it is neither evidence for the repair nor proof that it can go.
func (r *ValkeyReconciler) dataOwnershipRepairNeeded(ctx context.Context, v *vkov1.Valkey,
	current *appsv1.StatefulSet) bool {
	if !stsIsPersistent(current) || current.Spec.Replicas == nil {
		return false
	}
	if !templateRunsRootless(&current.Spec.Template.Spec) {
		return true
	}
	carried := builder.HasDataOwnershipRepair(&current.Spec.Template.Spec)
	allMigrated := true
	for i := int32(0); i < *current.Spec.Replicas; i++ {
		pod := &corev1.Pod{}
		key := types.NamespacedName{Name: fmt.Sprintf("%s-%d", current.Name, i), Namespace: v.Namespace}
		if err := r.Get(ctx, key, pod); err != nil || !podIsOurs(pod, current) {
			allMigrated = false
			continue
		}
		if !builder.PodRunsRootless(pod) {
			return true
		}
		if !podPassedPreflight(pod) {
			allMigrated = false
		}
	}
	return carried && !allMigrated
}

// stsIsPersistent reports whether the persisted StatefulSet keeps its data on
// claims. It is the fact the pods were built from; spec.persistence is only what the
// CR asks for, and a toggle of it is refused rather than applied (ADR 0023).
func stsIsPersistent(sts *appsv1.StatefulSet) bool {
	return len(sts.Spec.VolumeClaimTemplates) > 0
}

// templateRunsRootless is PodRunsRootless for a pod template.
func templateRunsRootless(spec *corev1.PodSpec) bool {
	sc := spec.SecurityContext
	return sc != nil && sc.RunAsNonRoot != nil && *sc.RunAsNonRoot
}

// podPassedPreflight reports whether a pod got past the ownership repair: its
// check-data-writable init container exited 0, or the pod has been Ready (which it
// cannot be without that). Either proves its volume is writable by uid 999.
func podPassedPreflight(pod *corev1.Pod) bool {
	if isPodReady(pod) {
		return true
	}
	for _, st := range pod.Status.InitContainerStatuses {
		if st.Name == builder.DataWritableCheckContainerName && st.State.Terminated != nil &&
			st.State.Terminated.ExitCode == 0 {
			return true
		}
		if st.Name == builder.DataWritableCheckContainerName && st.LastTerminationState.Terminated != nil &&
			st.LastTerminationState.Terminated.ExitCode == 0 {
			return true
		}
	}
	return false
}

// singlePodDeferral decides whether the only data pod of a spec.replicas: 1
// cluster keeps running on an outdated spec instead of being replaced, and on
// whose account. It returns the pod name in rootPending when the pod runs as root
// and replacing it would discard the dataset (ADR 0032 D3), and in sidecarPending
// when its sidecar image is the only drift (ADR 0007 D6); both empty means the pod
// is replaced. Every input comes from the persisted StatefulSet, never from the CR:
// a persistence toggle the operator refused to write (ADR 0023) would otherwise
// read as "persistent" and delete the only pod together with its emptyDir.
//
// The image-only isSidecarOnlyChange no longer decides a root pod. A release ships
// a new sidecar image together with the new posture, so on the Helm path that
// test classified the fix as sidecar-only and deferred it; on kustomize or a
// floating tag the sidecar does not move and the only pod was deleted at once --
// for a non-persistent cluster with its data (ADR 0007 D7 foresaw this). The line
// now sits where a restart turns from downtime into data loss:
//
//   - persistent: replaced now, with the ownership repair on its way up. One
//     restart, data kept.
//   - not persistent, Valkey image unchanged: deferred. The operator upgrade alone
//     never discards a dataset.
//   - not persistent, Valkey image changed: replaced, because the CR author asked
//     for a new image -- the same data-loss change that was always applied.
//   - not persistent, TLS material or configuration changed: replaced as well. Both
//     are records outside the pod-spec hash, so the operator upgrade alone never
//     moves them; a certificate rotation roll of a non-persistent single pod is the
//     data loss ADR 0030 already accepted, and a configuration change is the CR
//     author's. A change the pod-spec hash carries cannot be told apart from the
//     posture and is held with it -- the condition message says so.
func singlePodDeferral(v *vkov1.Valkey, sts *appsv1.StatefulSet, pod *corev1.Pod) (
	rootPending, sidecarPending string) {
	if v.Spec.Replicas > 1 {
		return "", ""
	}
	desiredImage := valkeyImageFromSts(sts)
	sidecarOnly := isSidecarOnlyChange(pod, desiredImage, sidecarImageFromSts(sts))
	if builder.PodRunsRootless(pod) {
		if sidecarOnly {
			return "", pod.Name
		}
		return "", ""
	}
	if stsIsPersistent(sts) || podImageChanged(pod, desiredImage, "") ||
		podTLSMaterialHashChanged(pod, tlsMaterialHashFromSts(sts)) ||
		podAnnotationHashChanged(pod, configHashFromSts(sts)) {
		return "", ""
	}
	if sidecarOnly {
		return pod.Name, pod.Name
	}
	return pod.Name, ""
}

// reportPodSecurityUpdatePending is the one evaluator of PodSecurityUpdatePending,
// called from checkAndHandleRollingUpdate on every non-error pass: True naming the
// pod whose replacement is deferred, otherwise a retraction of a standing True.
// Presence-guarded, so no cluster gains the condition from an upgrade that did not
// defer anything on it.
func (r *ValkeyReconciler) reportPodSecurityUpdatePending(ctx context.Context, v *vkov1.Valkey, pod string) {
	if pod != "" {
		r.setStatusCondition(ctx, v,
			vkov1.ConditionTypePodSecurityUpdatePending,
			metav1.ConditionTrue,
			vkov1.ReasonPodRunsAsRoot,
			fmt.Sprintf("Pod %s was built by an earlier operator version and runs as root. spec.replicas is 1 and "+
				"its StatefulSet keeps no volume, so replacing it would discard the dataset; the rootless posture, "+
				"and every other pending change of the pod spec, applies on its next restart. Delete the pod to "+
				"apply them now, together with the data",
				pod))
		return
	}
	cond := meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypePodSecurityUpdatePending)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return
	}
	r.setStatusCondition(ctx, v,
		vkov1.ConditionTypePodSecurityUpdatePending,
		metav1.ConditionFalse,
		vkov1.ReasonPodSecurityUpdateApplied,
		"No data pod keeps running as root on a deferred update")
}
