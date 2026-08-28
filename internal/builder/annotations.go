package builder

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

// AnnotationPodSpecHash is the annotation key used to store a hash of the
// generated pod spec (containers, resources, probes, volumes, etc.).
// It is embedded in the StatefulSet pod template so that pod-spec-level
// changes (e.g. resource requests/limits) are detected by the rolling
// update logic, even though the StatefulSet uses OnDelete strategy.
const AnnotationPodSpecHash = "vko.gtrfc.com/pod-spec-hash"

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

// AnnotationNudge is the annotation key the operator bumps on a StatefulSet that
// reports fewer pods than requested. Writing it changes the object's
// resourceVersion, which the statefulset-controller's informer turns into an
// immediate sync instead of the controller waiting out its exponential workqueue
// backoff (measured at 5 min 29 s after a transient admission-webhook rejection
// had already been resolved).
//
// The annotation lives on the StatefulSet metadata, not on the pod template, so
// it is invisible to StatefulSetHasChanged / SentinelStatefulSetHasChanged and
// never causes a rolling update.
const AnnotationNudge = "vko.gtrfc.com/nudge"

// NudgeInterval is the minimum time between two nudge bumps of the same
// StatefulSet. The stored timestamp doubles as the rate-limit state, so the
// operator needs no additional in-cluster state for it.
const NudgeInterval = 20 * time.Second

// NudgeDue reports whether the nudge annotation on obj may be bumped at now.
// It is due when the annotation is absent, unparsable, in the future (clock skew
// or a hand-edited value, which would otherwise block nudges forever), or older
// than NudgeInterval.
func NudgeDue(obj metav1.Object, now time.Time) bool {
	raw := obj.GetAnnotations()[AnnotationNudge]
	if raw == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, raw)
	if err != nil || last.After(now) {
		return true
	}
	return !now.Before(last.Add(NudgeInterval))
}

// NudgePatch returns a JSON merge patch setting the nudge annotation to now.
// A merge patch is used instead of an Update so the operator never overwrites
// concurrent changes to the rest of the StatefulSet.
func NudgePatch(now time.Time) []byte {
	return []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		AnnotationNudge, now.UTC().Format(time.RFC3339)))
}

// AnnotationTLSMaterialHash is the **superseded** carrier of the TLS material
// fingerprint, kept because pods created before 2026-08-27 still hold it.
//
// The operator no longer writes it. It carried a value the operator then trusted,
// and pod metadata is patchable by anything holding the sidecar token, so a
// single merge patch setting the key to null made the pod unmeasured -- no
// collision needed, and no digest strength would have helped. The fingerprint
// moved into the pod spec, which the API server refuses to change after creation
// (TLSMaterialHashEnvName, ADR 0031).
//
// RecordedTLSMaterialHash is the only reader left, and it consults this key only
// for a pod that carries no env. That fallback is self-extinguishing: a pod it
// fires on is a pod the roll then replaces with one that carries the env.
const AnnotationTLSMaterialHash = "vko.gtrfc.com/tls-material-hash"
