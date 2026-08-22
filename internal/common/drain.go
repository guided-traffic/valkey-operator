package common

// The drain signal is the contract between the Valkey container and the sidecar
// during pod termination.
//
// The kubelet sends SIGTERM to every container of a pod in one batch and gives no
// ordering guarantee. A Valkey configured with `save ""` exits within
// milliseconds, while the drain handler needs it alive: its first act is to read
// its own role from the local instance, and its peers only qualify as promotion
// targets (isSyncedReplica) while the master they replicate from still answers.
// A drain that loses that race promotes nobody, stamps nothing, and the returning
// pod self-claims the master role from the replica ConfigMap -- with an empty
// dataset when persistence is off, which the replicas then full-resync away.
//
// A preStop hook on the Valkey container waits for DrainCompleteFile to appear in
// DrainSignalMountPath, so the drain handler decides when Valkey may exit. Both
// sides are bounded: the hook gives up after a fixed timeout well inside
// terminationGracePeriodSeconds, and the sidecar writes the file on every exit
// path of its handler.
//
// These constants live in internal/common for the reason AnnotationDrainPromotedAt
// gives: it is the only package both internal/sidecar and internal/builder can
// share without pulling the API type tree into the sidecar binary.
const (
	// DrainSignalMountPath is the emptyDir shared by the Valkey container and the
	// sidecar. Its presence in the sidecar is what marks the handshake as active
	// for a pod: the operator only mounts it where the drain performs a manual
	// failover, so a missing directory means "not this cluster", not "broken".
	DrainSignalMountPath = "/var/run/vko"

	// DrainCompleteFile is written by the drain handler when it is done and is the
	// file the Valkey preStop hook waits for.
	DrainCompleteFile = "drain-complete"
)
