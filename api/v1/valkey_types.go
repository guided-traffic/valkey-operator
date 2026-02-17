package v1

import (
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

	// PodLabels are additional labels applied to Valkey pods.
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// PodAnnotations are additional annotations applied to Valkey pods.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// Resources defines the compute resource requirements for Valkey containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
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

func init() {
	SchemeBuilder.Register(&Valkey{}, &ValkeyList{})
}
