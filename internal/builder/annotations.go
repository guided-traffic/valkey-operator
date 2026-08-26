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

// AnnotationTLSMaterialHash is the annotation key used to store a fingerprint of
// the TLS Secret contents a pod was created with. Unlike the config and pod-spec
// hashes it is not computed by a builder: the fingerprint is Secret *content*,
// and buildPodSpec only ever sees the Valkey CR, which names the Secret and never
// its data. The reconciler stamps it onto the pod template instead.
//
// It exists because nothing else makes a certificate rotation visible to a pod.
// The Secret is remounted in place, and processes that parsed their TLS material
// at startup -- valkey-server, valkey-sentinel and the third-party metrics
// exporter -- keep using the material they parsed until they exit. A changed
// fingerprint changes the pod template, which the failover-aware rolling update
// then acts on, so those processes are replaced while the previous certificate is
// still valid.
//
// The operator's own long-lived processes are exempt from this mechanism by being
// able to re-read their material (internal/tlsmaterial); the observer Deployment
// therefore carries no fingerprint at all.
const AnnotationTLSMaterialHash = "vko.gtrfc.com/tls-material-hash"
