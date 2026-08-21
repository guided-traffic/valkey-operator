package v1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PersistenceMode defines the persistence strategy for Valkey.
// +kubebuilder:validation:Enum=rdb;aof;both
type PersistenceMode string

const (
	// PersistenceModeRDB enables RDB snapshotting.
	PersistenceModeRDB PersistenceMode = "rdb"
	// PersistenceModeAOF enables append-only file persistence.
	PersistenceModeAOF PersistenceMode = "aof"
	// PersistenceModeBoth enables both RDB and AOF persistence.
	PersistenceModeBoth PersistenceMode = "both"
)

// ConditionType is the type identifier for a status condition.
type ConditionType = string

const (
	// ConditionTypeSidecarUpdatePending is set on standalone Valkey instances when
	// the sidecar container image has drifted from the desired version.
	// Standalone pods are not automatically restarted for sidecar-only changes;
	// the update will occur on the next pod restart (manual delete or image change).
	ConditionTypeSidecarUpdatePending ConditionType = "SidecarUpdatePending"

	// ConditionTypeRollingUpdatePaused is set when a rolling update is paused
	// because a replaced pod failed to sync within the configured timeout.
	// The operator will not resume until the user applies a new spec change.
	ConditionTypeRollingUpdatePaused ConditionType = "RollingUpdatePaused"

	// ConditionTypeTopologyRestored reports whether the multi-replica rolling
	// update managed to hand the master role back to pod-0. It is set to True
	// once pod-0 has been promoted again, and to False when the operator gave up
	// waiting for pod-0 to sync and left the promoted replica as master. The
	// cluster is healthy in both cases -- the Services select the master by label,
	// not by ordinal -- so the condition is the only durable record that the
	// topology differs from the canonical one.
	ConditionTypeTopologyRestored ConditionType = "TopologyRestored"

	// ConditionTypeReconcileBlocked is set when the operator could not write one
	// of the managed resources. Its reason distinguishes an admission-webhook
	// rejection (a cluster-side gate, e.g. a fail-closed policy webhook whose
	// backend is down) from any other write failure, so users do not have to read
	// operator logs to tell the two apart.
	ConditionTypeReconcileBlocked ConditionType = "ReconcileBlocked"
)

const (
	// ReasonAdmissionWebhookDenied is the ReconcileBlocked reason for a write
	// rejected by the admission chain — either an explicit webhook denial or a
	// fail-closed webhook that could not be called. The condition message carries
	// the API server error including the webhook name.
	ReasonAdmissionWebhookDenied = "AdmissionWebhookDenied"

	// ReasonWriteFailed is the ReconcileBlocked reason for any other failure to
	// write a managed resource (RBAC, quota, conflict, API server unreachable).
	ReasonWriteFailed = "WriteFailed"

	// ReasonReconcileSucceeded clears ReconcileBlocked after a fully successful
	// reconcile pass over all managed resources.
	ReasonReconcileSucceeded = "ReconcileSucceeded"
)

// ValkeyPhase describes the current phase of the Valkey instance.
type ValkeyPhase string

const (
	// ValkeyPhaseOK indicates a healthy instance.
	ValkeyPhaseOK ValkeyPhase = "OK"
	// ValkeyPhaseProvisioning indicates the instance is being set up.
	ValkeyPhaseProvisioning ValkeyPhase = "Provisioning"
	// ValkeyphaseSyncing indicates replication is in progress.
	ValkeyphaseSyncing ValkeyPhase = "Syncing"
	// ValkeyPhaseRollingUpdate indicates a rolling update is in progress.
	ValkeyPhaseRollingUpdate ValkeyPhase = "Rolling Update"
	// ValkeyPhaseFailover indicates a failover is in progress.
	ValkeyPhaseFailover ValkeyPhase = "Failover in progress"
	// ValkeyPhaseError indicates an error state.
	ValkeyPhaseError ValkeyPhase = "Error"
)

// SentinelSpec defines the Sentinel configuration embedded in the Valkey CRD.
type SentinelSpec struct {
	// Enabled activates Sentinel-based HA mode.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Replicas is the number of Sentinel instances to run. Use an odd count of 3
	// or more: a failover needs floor(replicas/2)+1 Sentinels to agree, so 2
	// Sentinels tolerate no outage at all. With spec.podDisruptionBudget.enabled
	// that even count also blocks node drains, because the quorum then equals the
	// replica count and the Eviction API refuses every eviction; the operator
	// warns and records a SentinelPodDisruptionBudgetBlocksDrains Event on every
	// reconcile while that holds.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	Replicas int32 `json:"replicas,omitempty"`

	// PodLabels are additional labels applied to Sentinel pods.
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// PodAnnotations are additional annotations applied to Sentinel pods.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// AllowUnencrypted keeps the plaintext Sentinel port (26379) open alongside the TLS port (36379).
	// Only effective when spec.tls.enabled is true. Default: false.
	// +kubebuilder:default=false
	// +optional
	AllowUnencrypted bool `json:"allowUnencrypted,omitempty"`

	// DisableAuth disables password authentication for Sentinel client connections.
	// When true, Sentinel does not require a password from connecting clients
	// (no requirepass directive), but still uses sentinel auth-pass to
	// authenticate with Valkey nodes. Only effective when spec.auth is configured.
	// Default: false (Sentinel requires the same password as Valkey).
	// +kubebuilder:default=false
	// +optional
	DisableAuth bool `json:"disableAuth,omitempty"`
}

// AuthSpec defines authentication configuration for Valkey.
type AuthSpec struct {
	// SecretName is the name of the Kubernetes Secret containing the password.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// SecretPasswordKey is the key within the Secret that holds the password.
	// +kubebuilder:default=password
	// +optional
	SecretPasswordKey string `json:"secretPasswordKey,omitempty"`
}

// CertManagerIssuerSpec defines the cert-manager issuer reference.
type CertManagerIssuerSpec struct {
	// Group is the API group of the issuer (defaults to cert-manager.io).
	// +kubebuilder:default="cert-manager.io"
	// +optional
	Group string `json:"group,omitempty"`

	// Kind is the kind of the issuer (Issuer or ClusterIssuer).
	// +kubebuilder:validation:Enum=Issuer;ClusterIssuer
	Kind string `json:"kind"`

	// Name is the name of the issuer resource.
	Name string `json:"name"`
}

// CertManagerSpec defines the cert-manager configuration.
type CertManagerSpec struct {
	// Issuer references the cert-manager issuer to use.
	Issuer CertManagerIssuerSpec `json:"issuer"`

	// ExtraDNSNames specifies additional DNS names to include in the certificate
	// beyond the automatically generated ones (pod DNS names, service names).
	// +optional
	ExtraDNSNames []string `json:"extraDnsNames,omitempty"`
}

// TLSSpec defines TLS configuration for Valkey.
type TLSSpec struct {
	// Enabled activates TLS encryption.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// CertManager configures cert-manager integration for automatic certificate management.
	// Mutually exclusive with SecretName.
	// +optional
	CertManager *CertManagerSpec `json:"certManager,omitempty"`

	// SecretName references an existing Kubernetes Secret containing TLS certificates.
	// The Secret must contain keys: tls.crt, tls.key, and ca.crt.
	// Mutually exclusive with CertManager.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// AllowUnencrypted keeps the plaintext Valkey port (6379) open alongside the TLS port (16379).
	// Internal replication always uses TLS. Default: false (plaintext port disabled when TLS is active).
	// +kubebuilder:default=false
	// +optional
	AllowUnencrypted bool `json:"allowUnencrypted,omitempty"`

	// UnifiedCertificate, when true, makes Valkey and Sentinel share a single
	// TLS certificate / Secret that covers both sets of hostnames. With
	// cert-manager, the operator issues one Certificate (instead of one per
	// component) and migrates existing clusters by deleting the legacy
	// <name>-sentinel-tls Certificate and Secret once the Sentinel StatefulSet
	// has switched to the unified Secret. With a user-provided Secret, both
	// StatefulSets already mount that Secret, so the flag is informational.
	// This avoids TLS verification errors in clients (e.g., go-redis Sentinel
	// mode) that share one tls.Config across the Sentinel discovery and the
	// Valkey master connection.
	// +kubebuilder:default=false
	// +optional
	UnifiedCertificate bool `json:"unifiedCertificate,omitempty"`
}

const (
	// DefaultMetricsExporterImage is the exporter image used when spec.metrics.image is empty.
	// oliver006/redis_exporter supports Valkey and exposes standard Redis/Valkey metrics.
	DefaultMetricsExporterImage = "oliver006/redis_exporter:v1.66.0"

	// DefaultMetricsExporterPort is the default port the exporter serves /metrics on.
	DefaultMetricsExporterPort int32 = 9121

	// DefaultMetricsScrapeInterval is the default ServiceMonitor scrape interval.
	DefaultMetricsScrapeInterval = "30s"
)

// MetricsSpec defines the metrics exporter (Prometheus) configuration.
type MetricsSpec struct {
	// Enabled activates the metrics exporter sidecar.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Image is the exporter container image. When empty, a sensible default
	// (a redis_exporter release that also supports Valkey) is used.
	// +optional
	Image string `json:"image,omitempty"`

	// Port is the container/Service port the exporter serves /metrics on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// Resources defines the compute resource requirements for the exporter container.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// ExtraArgs are additional command-line arguments passed to the exporter.
	// +optional
	ExtraArgs []string `json:"extraArgs,omitempty"`

	// Service configures the dedicated metrics Service.
	// The Service is enabled by default when metrics are enabled.
	// +optional
	Service *MetricsServiceSpec `json:"service,omitempty"`

	// ServiceMonitor configures a Prometheus-Operator ServiceMonitor.
	// Requires the monitoring.coreos.com CRDs to be installed in the cluster;
	// the operator only creates the ServiceMonitor when this is enabled.
	// +optional
	ServiceMonitor *ServiceMonitorSpec `json:"serviceMonitor,omitempty"`
}

// MetricsServiceSpec configures the dedicated metrics Service that exposes the
// exporter port for scraping.
type MetricsServiceSpec struct {
	// Enabled controls creation of the metrics Service.
	// Defaults to true when metrics are enabled.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Labels are additional labels applied to the metrics Service.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// ServiceMonitorSpec configures a Prometheus-Operator ServiceMonitor for the exporter.
type ServiceMonitorSpec struct {
	// Enabled activates ServiceMonitor creation. Requires the Prometheus-Operator CRDs.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Interval at which Prometheus scrapes the exporter (e.g. "30s").
	// +kubebuilder:default="30s"
	// +optional
	Interval string `json:"interval,omitempty"`

	// ScrapeTimeout is the per-scrape timeout (e.g. "10s").
	// When empty, Prometheus' own default is used.
	// +optional
	ScrapeTimeout string `json:"scrapeTimeout,omitempty"`

	// Labels are additional labels applied to the ServiceMonitor, commonly used to
	// match a Prometheus instance's serviceMonitorSelector (e.g. release: prometheus).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

const (
	// DefaultPDBMaxUnavailable is the maxUnavailable used for the data
	// PodDisruptionBudget when spec.podDisruptionBudget.maxUnavailable is unset.
	DefaultPDBMaxUnavailable int32 = 1

	// MinPDBReplicas is the smallest replica count that gets a PodDisruptionBudget.
	// A StatefulSet with a single pod is never covered: maxUnavailable=1 would
	// permit evicting the only pod (a useless object) and minAvailable=1 would
	// block node drains forever (fake safety — a singleton is not HA either way).
	MinPDBReplicas int32 = 2
)

// PodDisruptionBudgetSpec configures PodDisruptionBudgets for the Valkey data
// and Sentinel StatefulSets. Omit the whole block to keep the operator out of
// PDBs entirely — users who manage their own PDB for these pods would otherwise
// end up with two budgets matching the same pods, which makes the Eviction API
// refuse every eviction.
//
// The operator only ever touches budgets it created, recognised by the
// ownerReference on them. A PodDisruptionBudget carrying one of the generated
// names (the StatefulSet names) that the operator does not own is neither deleted
// when the feature is off nor overwritten when it is on: it is left untouched and
// reported as a PodDisruptionBudgetNotOwned Warning Event on the CR, and this spec
// has no effect for that StatefulSet until the foreign budget is removed.
type PodDisruptionBudgetSpec struct {
	// Enabled creates a PDB for the data StatefulSet (maxUnavailable, default 1)
	// and, when Sentinel is enabled, a quorum-preserving PDB
	// (minAvailable = floor(replicas/2)+1) for the Sentinel StatefulSet.
	// StatefulSets with fewer than 2 replicas never get a PDB: it would either be
	// useless (maxUnavailable 1) or block node drains (minAvailable 1).
	// With exactly 2 Sentinels the quorum equals the replica count, so the Sentinel
	// budget permits no voluntary disruption at all and a node drain hosting a
	// Sentinel pod stalls until it is resolved manually; the operator keeps the
	// quorum formula (a smaller minAvailable would let a drain take automatic
	// failover) and warns about it on every reconcile.
	// A PodDisruptionBudget under one of those names that the operator does not own
	// is never deleted and never adopted: it stays untouched, this field has no
	// effect for that StatefulSet, and the operator records a
	// PodDisruptionBudgetNotOwned Event on every reconcile while that holds.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// MaxUnavailable is the maximum number of data pods that may be disrupted
	// voluntarily at the same time. It applies to the data StatefulSet only — the
	// Sentinel budget is always derived from the quorum and is not configurable,
	// because a settable value could silently break the quorum guarantee.
	// A value greater than or equal to spec.replicas disables the protection
	// (every pod may be evicted at once); the operator logs a warning and records a
	// PodDisruptionBudgetTooPermissive Event on every reconcile but honours it
	// rather than rejecting a later scale-down. Scaling spec.replicas down into
	// that condition is warned about as well, not only a change of this field.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxUnavailable *int32 `json:"maxUnavailable,omitempty"`
}

const (
	// AntiAffinityModeOff renders no anti-affinity term at all. It is the
	// default: an operator upgrade must not change the scheduling behavior of
	// existing clusters, so spreading is strictly opt-in.
	AntiAffinityModeOff = "off"

	// AntiAffinityModeSoft renders preferredDuringSchedulingIgnoredDuringExecution:
	// the scheduler tries to spread pods but never leaves one Pending.
	AntiAffinityModeSoft = "soft"

	// AntiAffinityModeHard renders requiredDuringSchedulingIgnoredDuringExecution:
	// the spread is guaranteed, surplus pods stay Pending.
	AntiAffinityModeHard = "hard"

	// DefaultAntiAffinityTopologyKey is the spread domain used when
	// spec.antiAffinity.topologyKey is unset: one pod per node.
	DefaultAntiAffinityTopologyKey = "kubernetes.io/hostname"

	// MinAntiAffinityReplicas is the smallest replica count that gets an
	// anti-affinity term. A singleton has no peer to repel, so injecting one
	// would only change the pod-spec hash and restart the pod for nothing.
	MinAntiAffinityReplicas int32 = 2

	// AntiAffinityWeight is the weight of the preferred (soft) anti-affinity term.
	// 100 is the maximum, making the spread the strongest preference the
	// scheduler weighs against its other priorities.
	AntiAffinityWeight int32 = 100
)

// AntiAffinitySpec configures pod anti-affinity for the data and Sentinel
// StatefulSets. Each StatefulSet repels only its own kind, so data and Sentinel
// pods may still share a node. A term is rendered only when mode is soft or
// hard AND the StatefulSet has at least MinAntiAffinityReplicas replicas;
// omitting the block (or mode: off, the default) renders nothing, so an
// operator upgrade never changes the scheduling of existing clusters. Without
// a term all pods of a cluster may land on one node — the enabling condition
// of the 2026-08-19 infra-d incident — so multi-replica clusters should opt
// into soft or hard.
type AntiAffinitySpec struct {
	// Mode selects off (no term, the default), soft
	// (preferredDuringSchedulingIgnoredDuringExecution) or hard
	// (requiredDuringSchedulingIgnoredDuringExecution).
	//
	// Soft is a scheduler preference: under node pressure pods may still be
	// co-located, so the spread is not guaranteed.
	//
	// Hard guarantees the spread and has two consequences worth knowing before
	// enabling it: with fewer schedulable nodes than replicas the surplus pods
	// stay Pending (which also wedges the next rolling update), and during a node
	// drain an evicted pod stays Pending until a node without a pod of the same
	// StatefulSet becomes schedulable.
	//
	// Switching between the modes changes the pod-spec hash and therefore
	// triggers one failover-aware rolling update.
	// +kubebuilder:validation:Enum=off;soft;hard
	// +kubebuilder:default=off
	// +optional
	Mode string `json:"mode,omitempty"`

	// TopologyKey is the node label whose values define the spread domains.
	// Defaults to kubernetes.io/hostname (one pod per node); set e.g.
	// topology.kubernetes.io/zone to spread across availability zones instead.
	// +kubebuilder:default="kubernetes.io/hostname"
	// +optional
	TopologyKey string `json:"topologyKey,omitempty"`
}

// NetworkPolicySpec defines network policy configuration.
type NetworkPolicySpec struct {
	// Enabled activates NetworkPolicy creation.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// NamePrefix is prepended to generated NetworkPolicy names.
	// +optional
	NamePrefix string `json:"namePrefix,omitempty"`
}

// ObserverLogLevel defines the log verbosity for the observer process.
// +kubebuilder:validation:Enum=debug;info;warn;error
type ObserverLogLevel string

const (
	// ObserverLogLevelDebug enables verbose logging including stack traces for all errors.
	ObserverLogLevelDebug ObserverLogLevel = "debug"
	// ObserverLogLevelInfo is the default level; expected check failures are logged without stack traces.
	ObserverLogLevelInfo ObserverLogLevel = "info"
	// ObserverLogLevelWarn suppresses info-level messages.
	ObserverLogLevelWarn ObserverLogLevel = "warn"
	// ObserverLogLevelError only emits error-level messages.
	ObserverLogLevelError ObserverLogLevel = "error"
)

// ObserverMTLSSpec controls whether the observer presents client certificates
// for its connections to Valkey and Sentinel. Only effective when spec.tls.enabled
// is true. Sending a client certificate enables mutual TLS (mTLS); omitting it
// means the observer only verifies the server certificate.
type ObserverMTLSSpec struct {
	// Valkey controls whether the observer sends a client certificate to Valkey pods.
	// Set to true to enable mTLS; when nil or false, no client certificate is sent.
	// Default: false.
	// +optional
	Valkey *bool `json:"valkey,omitempty"`

	// Sentinel controls whether the observer sends a client certificate to Sentinel pods.
	// When nil or false, only the CA certificate is used (no client certificate).
	// Set to true to enable mTLS for Sentinel connections.
	// Default: false.
	// +optional
	Sentinel *bool `json:"sentinel,omitempty"`
}

// ObserverUnreadyWhenSpec controls which check failures cause the observer
// to report unReady. When a field is false, the failure is still logged but
// the ready state is not affected by that check.
// All fields default to true.
type ObserverUnreadyWhenSpec struct {
	// MasterUnreachable: PING to the master node fails.
	// +kubebuilder:default=true
	// +optional
	MasterUnreachable *bool `json:"masterUnreachable,omitempty"`

	// WriteTestFailure: health key cannot be written to the master.
	// +kubebuilder:default=true
	// +optional
	WriteTestFailure *bool `json:"writeTestFailure,omitempty"`

	// ReadTestFailure: health key cannot be read back from the master.
	// +kubebuilder:default=true
	// +optional
	ReadTestFailure *bool `json:"readTestFailure,omitempty"`

	// ReplicaSyncFailure: a replica is disconnected or bulk sync is in progress.
	// +kubebuilder:default=true
	// +optional
	ReplicaSyncFailure *bool `json:"replicaSyncFailure,omitempty"`

	// ReplicaReadTestFailure: a replica returns stale or missing health key data.
	// +kubebuilder:default=true
	// +optional
	ReplicaReadTestFailure *bool `json:"replicaReadTestFailure,omitempty"`

	// SentinelUnreachable: one or more Sentinel instances do not respond to PING.
	// +kubebuilder:default=true
	// +optional
	SentinelUnreachable *bool `json:"sentinelUnreachable,omitempty"`

	// SentinelQuorumFailure: Sentinels disagree on the current master address.
	// +kubebuilder:default=true
	// +optional
	SentinelQuorumFailure *bool `json:"sentinelQuorumFailure,omitempty"`

	// SentinelMasterDown: Sentinel reports s_down or o_down flags on the master.
	// +kubebuilder:default=true
	// +optional
	SentinelMasterDown *bool `json:"sentinelMasterDown,omitempty"`

	// SentinelMasterHostnameInvalid: Sentinel reports a bare IP instead of a
	// DNS hostname for the master.
	// +kubebuilder:default=true
	// +optional
	SentinelMasterHostnameInvalid *bool `json:"sentinelMasterHostnameInvalid,omitempty"`

	// SentinelReplicaHostnamesInvalid: Sentinel reports bare IPs instead of
	// DNS hostnames for one or more replicas.
	// +kubebuilder:default=true
	// +optional
	SentinelReplicaHostnamesInvalid *bool `json:"sentinelReplicaHostnamesInvalid,omitempty"`
}

// ObserverSpec defines the observer configuration for cluster health monitoring.
type ObserverSpec struct {
	// Enabled activates the observer deployment.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// DB is the Valkey database number used for the health check key.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=15
	// +optional
	DB *int `json:"db,omitempty"`

	// MTLS configures mutual TLS behaviour for the observer's outbound connections.
	// Only effective when spec.tls.enabled is true.
	// +optional
	MTLS *ObserverMTLSSpec `json:"mtls,omitempty"`

	// Resources defines the compute resource requirements for the observer container.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// LogLevel sets the verbosity of observer log output.
	// At debug level, stack traces are included for all errors.
	// At info, warn, and error levels, stack traces are suppressed.
	// +kubebuilder:validation:Enum=debug;info;warn;error
	// +kubebuilder:default=info
	// +optional
	LogLevel ObserverLogLevel `json:"logLevel,omitempty"`

	// UnreadyWhen configures which check failures cause the observer to report
	// unReady. Omitting a field is equivalent to true (failure causes unReady).
	// Failures are always logged regardless of this setting.
	// +optional
	UnreadyWhen *ObserverUnreadyWhenSpec `json:"unreadyWhen,omitempty"`
}

// PersistenceSpec defines data persistence configuration.
type PersistenceSpec struct {
	// Enabled activates persistent storage for Valkey data.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Mode selects the persistence strategy: rdb, aof, or both.
	// +kubebuilder:default=rdb
	// +optional
	Mode PersistenceMode `json:"mode,omitempty"`

	// StorageClass is the name of the StorageClass to use. Empty string means default.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// Size is the requested storage size.
	// +kubebuilder:default="1Gi"
	// +optional
	Size resource.Quantity `json:"size,omitempty"`
}

// RollingUpdateSpec configures the behaviour of rolling updates.
type RollingUpdateSpec struct {
	// SyncTimeout is the maximum duration to wait for a replaced pod to
	// complete replication sync before pausing the rolling update.
	// Default: 5m.
	// +optional
	SyncTimeout *metav1.Duration `json:"syncTimeout,omitempty"`
}

// ValkeySpec defines the desired state of Valkey.
type ValkeySpec struct {
	// Replicas is the number of Valkey instances to run.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas,omitempty"`

	// Image is the Valkey container image to use.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Sentinel configures Sentinel-based HA mode.
	// +optional
	Sentinel *SentinelSpec `json:"sentinel,omitempty"`

	// Auth configures authentication.
	// +optional
	Auth *AuthSpec `json:"auth,omitempty"`

	// TLS configures TLS encryption.
	// +optional
	TLS *TLSSpec `json:"tls,omitempty"`

	// Metrics configures the metrics exporter.
	// +optional
	Metrics *MetricsSpec `json:"metrics,omitempty"`

	// NetworkPolicy configures network policies.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`

	// Persistence configures data persistence.
	// +optional
	Persistence *PersistenceSpec `json:"persistence,omitempty"`

	// Observer configures the observer deployment for cluster health monitoring.
	// +optional
	Observer *ObserverSpec `json:"observer,omitempty"`

	// PodLabels are additional labels applied to Valkey pods.
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// PodAnnotations are additional annotations applied to Valkey pods.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// Resources defines the compute resource requirements for Valkey containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// RollingUpdate configures rolling update behaviour.
	// +optional
	RollingUpdate *RollingUpdateSpec `json:"rollingUpdate,omitempty"`

	// PodDisruptionBudget configures PodDisruptionBudgets for the data and
	// Sentinel StatefulSets. Omitted means no PDBs are managed by the operator.
	// +optional
	PodDisruptionBudget *PodDisruptionBudgetSpec `json:"podDisruptionBudget,omitempty"`

	// AntiAffinity configures pod anti-affinity for the data and Sentinel
	// StatefulSets. Omitted means off: no term is rendered and scheduling is
	// unchanged. Set mode: soft or hard to spread StatefulSets with at least
	// two replicas across nodes.
	// +optional
	AntiAffinity *AntiAffinitySpec `json:"antiAffinity,omitempty"`
}

// ValkeyStatus defines the observed state of Valkey.
type ValkeyStatus struct {
	// ReadyReplicas is the number of ready Valkey instances.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// MasterPod is the name of the current master pod.
	// +optional
	MasterPod string `json:"masterPod,omitempty"`

	// Phase describes the current lifecycle phase of the Valkey cluster.
	// +optional
	Phase ValkeyPhase `json:"phase,omitempty"`

	// Message is a human-readable description of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// OperatorVersion is the version of the operator that last reconciled this resource.
	// +optional
	OperatorVersion string `json:"operatorVersion,omitempty"`

	// ObserverReady indicates whether the observer deployment is ready.
	// Only set when observer is enabled.
	// +optional
	ObserverReady *bool `json:"observerReady,omitempty"`

	// Conditions represent the latest available observations of the Valkey state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vk
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas",description="Desired number of Valkey replicas"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas",description="Number of ready replicas"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="Current phase"
// +kubebuilder:printcolumn:name="Master",type="string",JSONPath=".status.masterPod",description="Current master pod"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image",description="Valkey image",priority=1
// +kubebuilder:printcolumn:name="OperatorVersion",type="string",JSONPath=".status.operatorVersion",description="Operator version that last reconciled",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Valkey is the Schema for the valkeys API.
type Valkey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ValkeySpec   `json:"spec,omitempty"`
	Status ValkeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ValkeyList contains a list of Valkey.
type ValkeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Valkey `json:"items"`
}

// IsSentinelEnabled returns true if Sentinel HA mode is configured and enabled.
func (v *Valkey) IsSentinelEnabled() bool {
	return v.Spec.Sentinel != nil && v.Spec.Sentinel.Enabled
}

// IsMultiReplicaWithoutSentinel returns true when more than one replica is
// requested but Sentinel is not enabled. In this mode the operator uses a
// simple ordinal-based init container to assign pod-0 as master.
func (v *Valkey) IsMultiReplicaWithoutSentinel() bool {
	return v.Spec.Replicas > 1 && !v.IsSentinelEnabled()
}

// IsAuthEnabled returns true if authentication is configured.
func (v *Valkey) IsAuthEnabled() bool {
	return v.Spec.Auth != nil && v.Spec.Auth.SecretName != ""
}

// IsTLSEnabled returns true if TLS is configured and enabled.
func (v *Valkey) IsTLSEnabled() bool {
	return v.Spec.TLS != nil && v.Spec.TLS.Enabled
}

// IsCertManagerEnabled returns true if TLS is enabled and cert-manager is configured.
func (v *Valkey) IsCertManagerEnabled() bool {
	return v.IsTLSEnabled() && v.Spec.TLS.CertManager != nil
}

// IsUnifiedCertificateEnabled returns true if Valkey and Sentinel should share
// a single TLS certificate / Secret. With cert-manager this changes the issued
// Certificate to cover both hostname sets; with a user-provided Secret the flag
// is informational (both StatefulSets already mount the same Secret).
//
// With Sentinel disabled the flag still has one observable effect: it admits
// reconcileLegacySentinelCertificateCleanup, which garbage-collects the legacy <name>-sentinel-tls
// Certificate and Secret left behind by an instance that used to run Sentinel.
// sentinelRolloutComplete short-circuits to "complete" in that case (no pods are bound to the
// legacy Secret), so the cleanup runs on the first pass with no rollout to wait for. It only ever
// removes material it can prove is the operator's own — see deleteLegacySentinelSecret
// (docs/adr/0006-delete-only-what-the-operator-owns.md, D4-D11).
func (v *Valkey) IsUnifiedCertificateEnabled() bool {
	return v.IsTLSEnabled() && v.Spec.TLS.UnifiedCertificate
}

// IsTLSSecretProvided returns true if TLS is enabled with a user-provided Secret.
func (v *Valkey) IsTLSSecretProvided() bool {
	return v.IsTLSEnabled() && v.Spec.TLS.SecretName != ""
}

// IsMetricsEnabled returns true if metrics exporter is enabled.
func (v *Valkey) IsMetricsEnabled() bool {
	return v.Spec.Metrics != nil && v.Spec.Metrics.Enabled
}

// MetricsImage returns the exporter image, falling back to the default.
func (v *Valkey) MetricsImage() string {
	if v.Spec.Metrics != nil && v.Spec.Metrics.Image != "" {
		return v.Spec.Metrics.Image
	}
	return DefaultMetricsExporterImage
}

// MetricsPort returns the exporter port, falling back to the default.
func (v *Valkey) MetricsPort() int32 {
	if v.Spec.Metrics != nil && v.Spec.Metrics.Port != 0 {
		return v.Spec.Metrics.Port
	}
	return DefaultMetricsExporterPort
}

// IsServiceMonitorEnabled returns true if a Prometheus-Operator ServiceMonitor
// should be created for the exporter.
func (v *Valkey) IsServiceMonitorEnabled() bool {
	return v.IsMetricsEnabled() &&
		v.Spec.Metrics.ServiceMonitor != nil &&
		v.Spec.Metrics.ServiceMonitor.Enabled
}

// IsMetricsServiceEnabled returns true if the dedicated metrics Service should be
// created. It defaults to true when metrics are enabled. A ServiceMonitor requires
// the Service to scrape, so an enabled ServiceMonitor forces the Service on.
func (v *Valkey) IsMetricsServiceEnabled() bool {
	if !v.IsMetricsEnabled() {
		return false
	}
	if v.Spec.Metrics.Service != nil && v.Spec.Metrics.Service.Enabled != nil {
		return *v.Spec.Metrics.Service.Enabled || v.IsServiceMonitorEnabled()
	}
	return true
}

// MetricsScrapeInterval returns the ServiceMonitor scrape interval or the default.
func (v *Valkey) MetricsScrapeInterval() string {
	if v.Spec.Metrics != nil && v.Spec.Metrics.ServiceMonitor != nil &&
		v.Spec.Metrics.ServiceMonitor.Interval != "" {
		return v.Spec.Metrics.ServiceMonitor.Interval
	}
	return DefaultMetricsScrapeInterval
}

// IsNetworkPolicyEnabled returns true if network policies are enabled.
func (v *Valkey) IsNetworkPolicyEnabled() bool {
	return v.Spec.NetworkPolicy != nil && v.Spec.NetworkPolicy.Enabled
}

// IsPodDisruptionBudgetEnabled returns true if the operator should manage
// PodDisruptionBudgets for this instance.
func (v *Valkey) IsPodDisruptionBudgetEnabled() bool {
	return v.Spec.PodDisruptionBudget != nil && v.Spec.PodDisruptionBudget.Enabled
}

// PodDisruptionBudgetMaxUnavailable returns the maxUnavailable for the data PDB,
// falling back to the default.
func (v *Valkey) PodDisruptionBudgetMaxUnavailable() int32 {
	if v.Spec.PodDisruptionBudget != nil && v.Spec.PodDisruptionBudget.MaxUnavailable != nil {
		return *v.Spec.PodDisruptionBudget.MaxUnavailable
	}
	return DefaultPDBMaxUnavailable
}

// NeedsDataPodDisruptionBudget reports whether a PDB applies to the data
// StatefulSet: PDBs enabled and at least MinPDBReplicas pods.
func (v *Valkey) NeedsDataPodDisruptionBudget() bool {
	return v.IsPodDisruptionBudgetEnabled() && v.Spec.Replicas >= MinPDBReplicas
}

// NeedsSentinelPodDisruptionBudget reports whether a PDB applies to the Sentinel
// StatefulSet: PDBs enabled, Sentinel enabled and at least MinPDBReplicas pods.
func (v *Valkey) NeedsSentinelPodDisruptionBudget() bool {
	return v.IsPodDisruptionBudgetEnabled() && v.IsSentinelEnabled() &&
		v.Spec.Sentinel.Replicas >= MinPDBReplicas
}

// AntiAffinityMode returns the configured anti-affinity mode, falling back to
// the default (off). An unknown value is treated as off: the weakest setting —
// no constraint at all — is the safe fallback if validation is ever bypassed.
func (v *Valkey) AntiAffinityMode() string {
	if v.Spec.AntiAffinity == nil {
		return AntiAffinityModeOff
	}
	switch v.Spec.AntiAffinity.Mode {
	case AntiAffinityModeSoft:
		return AntiAffinityModeSoft
	case AntiAffinityModeHard:
		return AntiAffinityModeHard
	default:
		return AntiAffinityModeOff
	}
}

// IsAntiAffinityEnabled reports whether an anti-affinity term is requested at
// all (mode soft or hard).
func (v *Valkey) IsAntiAffinityEnabled() bool {
	return v.AntiAffinityMode() != AntiAffinityModeOff
}

// AntiAffinityTopologyKey returns the configured topology key, falling back to
// the default (kubernetes.io/hostname).
func (v *Valkey) AntiAffinityTopologyKey() string {
	if v.Spec.AntiAffinity != nil && v.Spec.AntiAffinity.TopologyKey != "" {
		return v.Spec.AntiAffinity.TopologyKey
	}
	return DefaultAntiAffinityTopologyKey
}

// NeedsDataAntiAffinity reports whether an anti-affinity term applies to the data
// StatefulSet: mode soft or hard, and at least MinAntiAffinityReplicas pods.
func (v *Valkey) NeedsDataAntiAffinity() bool {
	return v.IsAntiAffinityEnabled() && v.Spec.Replicas >= MinAntiAffinityReplicas
}

// NeedsSentinelAntiAffinity reports whether an anti-affinity term applies to the
// Sentinel StatefulSet: mode soft or hard, Sentinel enabled and at least
// MinAntiAffinityReplicas pods.
func (v *Valkey) NeedsSentinelAntiAffinity() bool {
	return v.IsAntiAffinityEnabled() && v.IsSentinelEnabled() &&
		v.Spec.Sentinel.Replicas >= MinAntiAffinityReplicas
}

// IsPersistenceEnabled returns true if persistence is configured and enabled.
func (v *Valkey) IsPersistenceEnabled() bool {
	return v.Spec.Persistence != nil && v.Spec.Persistence.Enabled
}

// IsValkeyUnencryptedAllowed returns true when TLS is enabled but the plaintext Valkey port (6379)
// should remain open alongside the TLS port (16379).
func (v *Valkey) IsValkeyUnencryptedAllowed() bool {
	return v.IsTLSEnabled() && v.Spec.TLS.AllowUnencrypted
}

// IsSentinelUnencryptedAllowed returns true when TLS is enabled but the plaintext Sentinel port (26379)
// should remain open alongside the TLS port (36379).
func (v *Valkey) IsSentinelUnencryptedAllowed() bool {
	return v.IsTLSEnabled() && v.IsSentinelEnabled() &&
		v.Spec.Sentinel != nil && v.Spec.Sentinel.AllowUnencrypted
}

// IsSentinelAuthDisabled returns true when auth is configured but Sentinel
// client connections should not require a password (no requirepass directive).
// Sentinel still uses sentinel auth-pass to authenticate with Valkey nodes.
func (v *Valkey) IsSentinelAuthDisabled() bool {
	return v.IsAuthEnabled() && v.IsSentinelEnabled() &&
		v.Spec.Sentinel.DisableAuth
}

// IsObserverEnabled returns true if the observer deployment is configured and enabled.
func (v *Valkey) IsObserverEnabled() bool {
	return v.Spec.Observer != nil && v.Spec.Observer.Enabled
}

// IsObserverValkeyMTLSEnabled returns true when the observer should send client
// certificates to Valkey pods. Defaults to false when not explicitly configured.
func (v *Valkey) IsObserverValkeyMTLSEnabled() bool {
	if v.Spec.Observer == nil || v.Spec.Observer.MTLS == nil || v.Spec.Observer.MTLS.Valkey == nil {
		return false
	}
	return *v.Spec.Observer.MTLS.Valkey
}

// IsObserverSentinelMTLSEnabled returns true when the observer should send client
// certificates to Sentinel pods. Defaults to false when not explicitly configured.
func (v *Valkey) IsObserverSentinelMTLSEnabled() bool {
	if v.Spec.Observer == nil || v.Spec.Observer.MTLS == nil || v.Spec.Observer.MTLS.Sentinel == nil {
		return false
	}
	return *v.Spec.Observer.MTLS.Sentinel
}

// IsObserverMTLSActive returns true when at least one of the observer's mTLS
// targets is enabled, meaning the TLS secret must be mounted.
func (v *Valkey) IsObserverMTLSActive() bool {
	return v.IsObserverValkeyMTLSEnabled() || v.IsObserverSentinelMTLSEnabled()
}

// GetObserverDB returns the Valkey database number for the observer health key.
func (v *Valkey) GetObserverDB() int {
	if v.Spec.Observer != nil && v.Spec.Observer.DB != nil {
		return *v.Spec.Observer.DB
	}
	return 15
}

// GetObserverLogLevel returns the configured observer log level, defaulting to "info".
func (v *Valkey) GetObserverLogLevel() string {
	if v.Spec.Observer != nil && v.Spec.Observer.LogLevel != "" {
		return string(v.Spec.Observer.LogLevel)
	}
	return "info"
}

// GetObserverResources returns the resource requirements for the observer container.
func (v *Valkey) GetObserverResources() corev1.ResourceRequirements {
	if v.Spec.Observer != nil && v.Spec.Observer.Resources != nil {
		return *v.Spec.Observer.Resources
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
	}
}

// GetSyncTimeout returns the configured sync timeout for rolling updates,
// defaulting to 5 minutes if not set.
func (v *Valkey) GetSyncTimeout() time.Duration {
	if v.Spec.RollingUpdate != nil && v.Spec.RollingUpdate.SyncTimeout != nil {
		return v.Spec.RollingUpdate.SyncTimeout.Duration
	}
	return 5 * time.Minute
}

// UnreadyWhenDefault returns the effective bool value for an unreadyWhen field.
// A nil pointer means "use default", which is true.
func UnreadyWhenDefault(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

// GetObserverUnreadyWhen returns the effective UnreadyWhen config,
// never nil — falls back to all-true defaults when spec.observer.unreadyWhen
// is not set.
func (v *Valkey) GetObserverUnreadyWhen() ObserverUnreadyWhenSpec {
	if v.Spec.Observer == nil || v.Spec.Observer.UnreadyWhen == nil {
		return ObserverUnreadyWhenSpec{} // all nil = all default true
	}
	return *v.Spec.Observer.UnreadyWhen
}
