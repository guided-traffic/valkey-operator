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
// It lives in internal/common because both sides use it: the sidecar drain
// handler writes it, the controller reads and clears it, and internal/common is
// where the other names both sides share (the labels) live.
const AnnotationDrainPromotedAt = "vko.gtrfc.com/drain-promoted-at"
