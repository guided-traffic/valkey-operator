package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// reportRWServiceEndpoints records, on a settled cluster, whether the -rw
// Service can select a master at all.
//
// The operator never writes the instanceRole label -- each pod's sidecar does
// (ADR 0012) -- so this is the one place the CR reports the sidecars failing at
// their single API write. The measured cause on a live fleet was every sidecar
// of a TLS cluster dead on expired client material (T21): all three pods
// labeled replica, the -rw Service empty, and every other status surface
// reading healthy (T7).
//
// It mutates v.Status.Conditions in place and writes nothing itself: both
// callers run between the prevStatus capture and persistStatus, so the verdict
// rides the status write the pass performs anyway and costs no extra Update.
//
// Judged only when every pod is ready and no rolling update is in flight --
// anything else has legitimate label-less windows. A brief True during a
// Sentinel failover on a fully ready fleet remains possible and is accepted,
// the same way MultipleMasters is briefly True during every controlled
// failover (ADR 0025). Upgrade-neutral in the T24(d) style: the clearing False
// is only written over an existing condition, so a cluster that never exhibits
// the state never gains the row.
func (r *ValkeyReconciler) reportRWServiceEndpoints(ctx context.Context, v *vkov1.Valkey, readyReplicas int32) {
	if v.Spec.Replicas < 1 || readyReplicas != v.Spec.Replicas || r.getRollingUpdateState(v) != "" {
		return
	}

	labeled, err := r.listMasterLabeledPods(ctx, v)
	if err != nil {
		log.FromContext(ctx).V(1).Info("cannot list master-labeled pods for the RWServiceEmpty report",
			"error", err.Error())
		return
	}

	if len(labeled) == 0 {
		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               vkov1.ConditionTypeRWServiceEmpty,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: v.Generation,
			Reason:             vkov1.ReasonNoPodLabeledMaster,
			Message: fmt.Sprintf("No data pod carries the %s=%s label, so the %s Service has no "+
				"endpoints and serves no writes. The sidecar labeler owns that label; check the "+
				"sidecar container logs on the data pods",
				common.LabelInstanceRole, common.RoleMaster, builder.RWServiceName(v)),
		})
		return
	}

	if meta.FindStatusCondition(v.Status.Conditions, vkov1.ConditionTypeRWServiceEmpty) == nil {
		return
	}
	meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
		Type:               vkov1.ConditionTypeRWServiceEmpty,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: v.Generation,
		Reason:             vkov1.ReasonMasterLabeled,
		Message:            "A data pod carries the master label again; the -rw Service has endpoints",
	})
}
