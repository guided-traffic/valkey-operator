package common

// AnnotationDrainPromotedAt is stamped by the sidecar drain handler on the pod
// it promoted while the local pod was terminating (SIGTERM, non-Sentinel path).
// The value is an RFC3339 UTC timestamp; an absent, empty or unparseable value
// means "no stamp".
//
// It exists because the sidecar has no access to the Valkey CR: every promotion
// the operator performs is recorded in the known-master annotation on the CR,
// but a promotion the drain handler performs is invisible to the operator. This
// pod annotation is the only trace of it, and the operator uses it to tell an
// unrecorded but legitimate promotion apart from a pod that elected itself.
//
// The constant lives here rather than next to the builder annotations because
// internal/common is the only package both internal/sidecar and
// internal/controller already import; putting it in internal/builder would pull
// the whole API type tree into the sidecar binary for one string.
const AnnotationDrainPromotedAt = "vko.gtrfc.com/drain-promoted-at"
