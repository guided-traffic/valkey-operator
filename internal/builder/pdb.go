package builder

import (
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// PodDisruptionBudgetName returns the name of the data PodDisruptionBudget.
// It mirrors the data StatefulSet name; PDBs are a separate resource kind, so
// there is no name collision.
func PodDisruptionBudgetName(v *vkov1.Valkey) string {
	return common.StatefulSetName(v, common.ComponentValkey)
}

// SentinelPodDisruptionBudgetName returns the name of the Sentinel PodDisruptionBudget.
func SentinelPodDisruptionBudgetName(v *vkov1.Valkey) string {
	return common.StatefulSetName(v, common.ComponentSentinel)
}

// BuildValkeyPodDisruptionBudget builds the PDB for the data StatefulSet. It caps
// the number of data pods a voluntary disruption (node drain, cluster autoscaler)
// may take down at once; the operator's own rolling update deletes pods directly
// and is therefore not affected.
func BuildValkeyPodDisruptionBudget(v *vkov1.Valkey) *policyv1.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt32(v.PodDisruptionBudgetMaxUnavailable())

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodDisruptionBudgetName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: common.SelectorLabels(v, common.ComponentValkey),
			},
		},
	}
}

// BuildSentinelPodDisruptionBudget builds the PDB for the Sentinel StatefulSet.
// minAvailable is the quorum (floor(replicas/2)+1), so a drain can never take the
// Sentinel majority — which would leave the cluster without automatic failover.
// With exactly 2 Sentinels the quorum equals the replica count, so no voluntary
// disruption is allowed at all; that is the honest consequence of running an even,
// non-HA Sentinel count.
func BuildSentinelPodDisruptionBudget(v *vkov1.Valkey) *policyv1.PodDisruptionBudget {
	minAvailable := intstr.FromInt32(SentinelQuorumFor(v.Spec.Sentinel.Replicas))

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SentinelPodDisruptionBudgetName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentSentinel),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: common.SelectorLabels(v, common.ComponentSentinel),
			},
		},
	}
}

// PodDisruptionBudgetHasChanged reports whether the live PDB differs from the
// desired one in the fields the operator owns. Both budget fields are compared, so
// switching between maxUnavailable and minAvailable counts as a change.
//
// Fields outside that set — notably unhealthyPodEvictionPolicy, which a cluster
// policy may inject — are deliberately ignored: comparing (and rewriting) the whole
// spec would turn such an injection into an endless operator-vs-webhook update loop.
func PodDisruptionBudgetHasChanged(desired, current *policyv1.PodDisruptionBudget) bool {
	return !equality.Semantic.DeepEqual(desired.Spec.MinAvailable, current.Spec.MinAvailable) ||
		!equality.Semantic.DeepEqual(desired.Spec.MaxUnavailable, current.Spec.MaxUnavailable) ||
		!equality.Semantic.DeepEqual(desired.Spec.Selector, current.Spec.Selector)
}

// ApplyPodDisruptionBudgetSpec copies the operator-managed fields of desired onto
// current, leaving the rest of the live spec untouched (see
// PodDisruptionBudgetHasChanged).
func ApplyPodDisruptionBudgetSpec(desired, current *policyv1.PodDisruptionBudget) {
	current.Spec.MinAvailable = desired.Spec.MinAvailable
	current.Spec.MaxUnavailable = desired.Spec.MaxUnavailable
	current.Spec.Selector = desired.Spec.Selector
}
