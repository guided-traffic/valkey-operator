package builder

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// BuildPodAntiAffinity builds the pod anti-affinity for one component's pods.
// The term repels only pods of the same component of the same Valkey CR, so data
// and Sentinel pods never repel each other and a second Valkey instance in the
// same namespace is unaffected.
//
// Returns nil when the component has fewer than MinAntiAffinityReplicas replicas:
// a singleton has no peer to repel, and an empty term would still change the
// pod-spec hash and restart the pod for nothing.
func BuildPodAntiAffinity(v *vkov1.Valkey, component string) *corev1.Affinity {
	if !needsAntiAffinity(v, component) {
		return nil
	}

	term := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: common.SelectorLabels(v, component),
		},
		TopologyKey: v.AntiAffinityTopologyKey(),
	}

	antiAffinity := &corev1.PodAntiAffinity{}
	if v.AntiAffinityMode() == vkov1.AntiAffinityModeHard {
		antiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = []corev1.PodAffinityTerm{term}
	} else {
		antiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.WeightedPodAffinityTerm{{
			Weight:          vkov1.AntiAffinityWeight,
			PodAffinityTerm: term,
		}}
	}

	return &corev1.Affinity{PodAntiAffinity: antiAffinity}
}

// needsAntiAffinity reports whether the given component's StatefulSet has enough
// replicas for an anti-affinity term to mean anything.
func needsAntiAffinity(v *vkov1.Valkey, component string) bool {
	if component == common.ComponentSentinel {
		return v.NeedsSentinelAntiAffinity()
	}
	return v.NeedsDataAntiAffinity()
}
