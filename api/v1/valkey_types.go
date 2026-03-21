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

	// Replicas is the number of Sentinel instances to run.
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
}

// MetricsSpec defines metrics/exporter configuration.
type MetricsSpec struct {
	// Enabled activates the metrics exporter sidecar.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
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

// IsTLSSecretProvided returns true if TLS is enabled with a user-provided Secret.
func (v *Valkey) IsTLSSecretProvided() bool {
	return v.IsTLSEnabled() && v.Spec.TLS.SecretName != ""
}

// IsMetricsEnabled returns true if metrics exporter is enabled.
func (v *Valkey) IsMetricsEnabled() bool {
	return v.Spec.Metrics != nil && v.Spec.Metrics.Enabled
}

// IsNetworkPolicyEnabled returns true if network policies are enabled.
func (v *Valkey) IsNetworkPolicyEnabled() bool {
	return v.Spec.NetworkPolicy != nil && v.Spec.NetworkPolicy.Enabled
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

func init() {
	SchemeBuilder.Register(&Valkey{}, &ValkeyList{})
}
