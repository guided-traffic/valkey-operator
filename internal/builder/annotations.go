package builder

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AnnotationOperatorVersion is the annotation key used to track which operator
// version last reconciled a managed resource. It is applied to all resources
// created or updated by the operator to provide an audit trail and enable
// detection of resources not yet reconciled by the current version.
const AnnotationOperatorVersion = "vko.gtrfc.com/operator-version"

// AnnotationConfigHash is the annotation key used to store a hash of the
// generated Valkey / Sentinel configuration content. It is embedded in the
// StatefulSet pod template so that config changes (e.g. toggling
// allowUnencrypted) are propagated as a pod template annotation change, which
// the operator's rolling update logic then detects and acts upon.
const AnnotationConfigHash = "vko.gtrfc.com/config-hash"

// ApplyOperatorVersion sets the operator-version annotation on a Kubernetes object.
// If version is empty the annotation is left unchanged.
func ApplyOperatorVersion(obj metav1.Object, version string) {
	if version == "" {
		return
	}
	anns := obj.GetAnnotations()
	if anns == nil {
		anns = make(map[string]string)
	}
	anns[AnnotationOperatorVersion] = version
	obj.SetAnnotations(anns)
}

// OperatorVersionChanged returns true when the annotation on current does not
// match version, indicating that the resource was last reconciled by a different
// operator version and should be updated.
func OperatorVersionChanged(current metav1.Object, version string) bool {
	if version == "" {
		return false
	}
	return current.GetAnnotations()[AnnotationOperatorVersion] != version
}
