package builder

import (
	"encoding/json"
	"fmt"
	"hash/fnv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// PodMetadataHashEnvName carries the pod metadata record on the pod: a digest of
// the labels and annotations the CR author supplies for the pod's tier, as an
// environment variable of the tier's carrier container (the sidecar on the data
// tier, the sentinel container on the Sentinel tier).
//
// The StatefulSets use OnDelete, so a podLabels or podAnnotations change rewrites
// the template and reaches no running pod; the rolling update compares this record
// to replace the pods instead (docs/adr/0007-failover-aware-rolling-update.md, D2).
// It lives in the pod spec for the reason TLSMaterialHashEnvName does: the sidecar
// may patch its own pod's metadata and nobody may change a pod's env, so a pod
// cannot delete its record to opt out of the replacement (ADR 0031 D1, D2).
//
// Nothing in the pod reads the variable. It is a record, not configuration.
const PodMetadataHashEnvName = "VKO_POD_METADATA_HASH"

// ComputePodMetadataHash returns a short hex digest of the pod labels and
// annotations a CR author supplies for one tier. Only the user-supplied maps
// enter it, never the merged ones, so an operator-owned label that changes with a
// release does not move it; json.Marshal sorts map keys, and an empty map hashes
// like an absent one.
//
// The value of a given pair of maps must never change between releases: a record
// that differs replaces even a single data pod whose sidecar deferral stands
// (ADR 0007 D6), so a new recipe would restart every non-persistent single pod at
// the upgrade, with its dataset. TestComputePodMetadataHash_Pinned guards it.
func ComputePodMetadataHash(labels, annotations map[string]string) string {
	data, _ := json.Marshal(struct {
		Labels      map[string]string `json:"labels,omitempty"`
		Annotations map[string]string `json:"annotations,omitempty"`
	}{labels, annotations})
	h := fnv.New32a()
	_, _ = h.Write(data)
	return fmt.Sprintf("%08x", h.Sum32())
}

// DataPodMetadataHash is the pod metadata record of the data tier.
func DataPodMetadataHash(v *vkov1.Valkey) string {
	return ComputePodMetadataHash(v.Spec.PodLabels, v.Spec.PodAnnotations)
}

// SentinelPodMetadataHash is the pod metadata record of the Sentinel tier.
func SentinelPodMetadataHash(v *vkov1.Valkey) string {
	if v.Spec.Sentinel == nil {
		return ComputePodMetadataHash(nil, nil)
	}
	return ComputePodMetadataHash(v.Spec.Sentinel.PodLabels, v.Spec.Sentinel.PodAnnotations)
}

// StampPodMetadataHash records hash on the named container of the StatefulSet's
// pod template, replacing any value already there. It runs on the built
// StatefulSet, like StampTLSMaterialHash and for the same reason: the pod-spec
// hash digests the spec the builder produced, and a metadata change must not move
// it too (ADR 0031 D3).
//
// Both tiers stamp it on every write, empty maps included: without a desired value
// the removal of the last entry would read as "cannot tell" (ADR 0007 D3) and roll
// nothing.
func StampPodMetadataHash(sts *appsv1.StatefulSet, containerName, hash string) {
	stampTemplateEnv(sts, containerName, PodMetadataHashEnvName, hash)
}

// RecordedPodMetadataHash returns the pod metadata record carried by a pod spec or
// a pod template spec, or the empty string when no container carries one.
func RecordedPodMetadataHash(spec *corev1.PodSpec) string {
	if spec == nil {
		return ""
	}
	for i := range spec.Containers {
		for _, env := range spec.Containers[i].Env {
			if env.Name == PodMetadataHashEnvName {
				return env.Value
			}
		}
	}
	return ""
}

// stampTemplateEnv sets the environment variable name to value on the named
// container of the StatefulSet's pod template, replacing an existing entry rather
// than appending a second one, which the API server would reject together with the
// whole StatefulSet write.
func stampTemplateEnv(sts *appsv1.StatefulSet, containerName, name, value string) {
	containers := sts.Spec.Template.Spec.Containers
	for i := range containers {
		if containers[i].Name != containerName {
			continue
		}
		for j := range containers[i].Env {
			if containers[i].Env[j].Name == name {
				containers[i].Env[j].Value = value
				return
			}
		}
		containers[i].Env = append(containers[i].Env, corev1.EnvVar{Name: name, Value: value})
		return
	}
}
