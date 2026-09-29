package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
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
// Once carried, it is kept until every ordinal of the tier holds a migrated pod --
// proven ours, rootless and Ready -- and no data-tier roll is recorded. A missing
// pod, or a rootless one that never got that far (an image it cannot pull, a node
// it cannot schedule on), does not count. The asymmetry closes two races the plain
// "any legacy pod exists" rule has on the last migration of a tier: a pass between
// the last legacy pod disappearing and its recreation, and a replacement that is
// rootless but has not run its repair yet.
//
// The removal outdates every pod that carries the repair, so it is what starts the
// second roll (ADR 0032 D2), and the two conditions order that roll behind the
// first. reconcileStatefulSet runs before the rolling update in the same pass, so a
// removal while the first roll is still recorded would outdate every pod under it:
// clearStaleRollingUpdateState would discard its state as stale -- on the
// non-Sentinel path in the middle of the topology restoration -- and its
// finalization would never run. A single pod records no roll state; Ready is what
// keeps its second restart behind the first having served.
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
	if carried && r.getRollingUpdateState(v) != "" {
		return true
	}
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
		if !isPodReady(pod) {
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

// podSecurityPending names the only data pod of a spec.replicas: 1 cluster whose
// replacement is held back although it carries a security repair, and which
// repair: PodRunsAsRoot for the rootless posture (ADR 0032 D3), ExporterOutdated
// for the exporter configuration of ADR 0018 D11. The zero value holds nothing.
type podSecurityPending struct {
	pod, reason string
}

// singlePodDeferral decides whether the only data pod of a spec.replicas: 1
// cluster keeps running on an outdated spec instead of being replaced, and on
// whose account. It returns the pod in securityPending when a security repair is
// held because replacing the pod would discard the dataset, and the pod name in
// sidecarPending when its sidecar image is the only image drift (ADR 0007 D6);
// both empty means the pod is replaced. Every input comes from the persisted
// StatefulSet, never from the CR: a persistence toggle the operator refused to
// write (ADR 0023) would otherwise read as "persistent" and delete the only pod
// together with its emptyDir.
//
// The image-only isSidecarOnlyChange does not decide a pod that carries a
// security repair. A release ships a new sidecar image together with the repair,
// so on the Helm path that test classified the repair as sidecar-only and
// deferred it; on kustomize or a floating tag the sidecar does not move and the
// only pod was deleted at once -- for a non-persistent cluster with its data
// (ADR 0007 D7 foresaw this). Two repairs are decided this way: a pod that runs as
// root (ADR 0032 D3), and a rootless pod whose exporter image or environment
// differs from the template (ADR 0018 D11). For both, the line sits where a
// restart turns from downtime into data loss:
//
//   - persistent: replaced now. One restart, data kept.
//   - not persistent, Valkey image unchanged: deferred. The operator upgrade alone
//     never discards a dataset.
//   - not persistent, Valkey image changed: replaced, because the CR author asked
//     for a new image -- the same data-loss change that was always applied.
//   - not persistent, TLS material or configuration changed: replaced as well. Both
//     are records outside the pod-spec hash, so the operator upgrade alone never
//     moves them; a certificate rotation roll of a non-persistent single pod is the
//     data loss ADR 0030 already accepted, and a configuration change is the CR
//     author's. A change the pod-spec hash carries cannot be told apart from the
//     repair and is held with it -- the condition message says so.
//
// A rootless pod without exporter drift is decided by isSidecarOnlyChange, as before.
func singlePodDeferral(v *vkov1.Valkey, sts *appsv1.StatefulSet, pod *corev1.Pod) (
	securityPending podSecurityPending, sidecarPending string) {
	if v.Spec.Replicas > 1 {
		return podSecurityPending{}, ""
	}
	desiredImage := valkeyImageFromSts(sts)
	if isSidecarOnlyChange(pod, desiredImage, sidecarImageFromSts(sts)) {
		sidecarPending = pod.Name
	}
	var reason string
	switch {
	case !builder.PodRunsRootless(pod):
		reason = vkov1.ReasonPodRunsAsRoot
	case exporterDrifted(pod, sts):
		reason = vkov1.ReasonExporterOutdated
	default:
		return podSecurityPending{}, sidecarPending
	}
	if singlePodReplaceable(pod, sts, desiredImage) {
		return podSecurityPending{}, ""
	}
	return podSecurityPending{pod: pod.Name, reason: reason}, sidecarPending
}

// singlePodReplaceable reports whether the only data pod is replaced although it
// carries a held repair: its StatefulSet keeps a volume, or a change the operator
// upgrade alone never makes -- the Valkey image, the TLS material record, the
// configuration -- replaces it anyway.
func singlePodReplaceable(pod *corev1.Pod, sts *appsv1.StatefulSet, desiredImage string) bool {
	return stsIsPersistent(sts) || podImageChanged(pod, desiredImage, "") ||
		podTLSMaterialHashChanged(pod, tlsMaterialHashFromSts(sts)) ||
		podAnnotationHashChanged(pod, configHashFromSts(sts))
}

// exporterDrifted reports whether the pod runs an exporter container whose image
// or environment differs from the persisted template's. A pod or template without
// one is no drift here: adding or removing the exporter is the CR author's change
// and rides the ordinary comparison.
func exporterDrifted(pod *corev1.Pod, sts *appsv1.StatefulSet) bool {
	want := containerNamed(sts.Spec.Template.Spec.Containers, builder.ExporterContainerName)
	got := containerNamed(pod.Spec.Containers, builder.ExporterContainerName)
	if want == nil || got == nil {
		return false
	}
	return want.Image != got.Image || !equality.Semantic.DeepEqual(want.Env, got.Env)
}

func containerNamed(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

// reportPodSecurityUpdatePending is the one evaluator of PodSecurityUpdatePending,
// called from checkAndHandleRollingUpdate on every non-error pass: True naming the
// pod whose replacement is deferred and the repair it waits for, otherwise a
// retraction of a standing True. Presence-guarded, so no cluster gains the
// condition from an upgrade that did not defer anything on it.
func (r *ValkeyReconciler) reportPodSecurityUpdatePending(ctx context.Context, v *vkov1.Valkey, pending podSecurityPending) {
	if pending.pod != "" {
		r.setStatusCondition(ctx, v,
			vkov1.ConditionTypePodSecurityUpdatePending,
			metav1.ConditionTrue,
			pending.reason,
			pendingMessage(pending))
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
		"No data pod keeps a security update deferred")
}

// pendingMessage is the PodSecurityUpdatePending message for a held repair.
func pendingMessage(pending podSecurityPending) string {
	state, update := "was built by an earlier operator version and runs as root", "the rootless posture"
	if pending.reason == vkov1.ReasonExporterOutdated {
		state = "runs the metrics exporter an earlier operator version configured, with routes this version " +
			"switches off"
		update = "the exporter update"
	}
	return fmt.Sprintf("Pod %s %s. spec.replicas is 1 and its StatefulSet keeps no volume, so replacing it would "+
		"discard the dataset; %s, and every other pending change of the pod spec, applies on its next restart. "+
		"Delete the pod to apply them now, together with the data", pending.pod, state, update)
}
