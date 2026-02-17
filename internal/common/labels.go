package common

import (
	"fmt"
	"strings"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

const (
	// Standard Kubernetes recommended labels.
	LabelComponent = "app.kubernetes.io/component"
	LabelInstance  = "app.kubernetes.io/instance"
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelName      = "app.kubernetes.io/name"
	LabelVersion   = "app.kubernetes.io/version"

	// Operator-specific labels.
	LabelCluster      = "vko.gtrfc.com/cluster"
	LabelInstanceName = "vko.gtrfc.com/instanceName"
	LabelInstanceRole = "vko.gtrfc.com/instanceRole"

	// Component values.
	ComponentValkey   = "valkey"
	ComponentSentinel = "sentinel"

	// Role values.
	RoleMaster  = "master"
	RoleReplica = "replica"

	// ManagedBy is the constant value for the managed-by label.
	ManagedBy = "vko.gtrfc.com"
)

// ExtractVersionFromImage extracts the tag portion from a container image string.
// Returns "latest" if no tag is present.
func ExtractVersionFromImage(image string) string {
	// Handle images with digest (e.g., "image@sha256:...")
	if idx := strings.LastIndex(image, "@"); idx != -1 {
		return image[idx+1:]
	}

	// Handle images with tag (e.g., "image:tag")
	// Account for registry port (e.g., "registry:5000/image:tag")
	lastSlash := strings.LastIndex(image, "/")
	tagPart := image
	if lastSlash != -1 {
		tagPart = image[lastSlash:]
	}

	if idx := strings.LastIndex(tagPart, ":"); idx != -1 {
		return tagPart[idx+1:]
	}

	return "latest"
}

// BaseLabels returns the common labels shared by all resources managed by the operator.
// These labels do NOT include pod-specific labels (instanceName, instanceRole).
func BaseLabels(v *vkov1.Valkey, component string) map[string]string {
	return map[string]string{
		LabelComponent: component,
		LabelInstance:  v.Name,
		LabelManagedBy: ManagedBy,
		LabelName:      "valkey",
		LabelVersion:   ExtractVersionFromImage(v.Spec.Image),
		LabelCluster:   v.Name,
	}
}

// PodLabels returns the full set of labels for a pod, including instance-specific labels.
// userLabels are merged in, but operator-managed labels take precedence to prevent overwrites.
func PodLabels(v *vkov1.Valkey, component, podName, role string, userLabels map[string]string) map[string]string {
	labels := make(map[string]string, len(userLabels)+8)

	// Copy user-defined labels first (lower precedence).
	for k, val := range userLabels {
		labels[k] = val
	}

	// Apply operator-managed labels (higher precedence).
	for k, val := range BaseLabels(v, component) {
		labels[k] = val
	}

	labels[LabelInstanceName] = podName
	labels[LabelInstanceRole] = role

	return labels
}

// SelectorLabels returns the minimal label set used for label selectors (e.g., Services, StatefulSets).
func SelectorLabels(v *vkov1.Valkey, component string) map[string]string {
	return map[string]string{
		LabelInstance:  v.Name,
		LabelManagedBy: ManagedBy,
		LabelComponent: component,
	}
}

// StatefulSetName returns the name for a Valkey or Sentinel StatefulSet.
func StatefulSetName(v *vkov1.Valkey, component string) string {
	if component == ComponentSentinel {
		return fmt.Sprintf("%s-sentinel", v.Name)
	}
	return v.Name
}

// HeadlessServiceName returns the name for a headless Service.
func HeadlessServiceName(v *vkov1.Valkey, component string) string {
	if component == ComponentSentinel {
		return fmt.Sprintf("%s-sentinel-headless", v.Name)
	}
	return fmt.Sprintf("%s-headless", v.Name)
}

// MergeLabels merges multiple label maps. Later maps take precedence over earlier ones.
func MergeLabels(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// MergeAnnotations merges multiple annotation maps. Later maps take precedence.
func MergeAnnotations(maps ...map[string]string) map[string]string {
	return MergeLabels(maps...) // Same logic applies.
}
