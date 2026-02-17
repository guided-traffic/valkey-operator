package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SentinelSpec defines the desired state of Sentinel.
type SentinelSpec struct {
	// Replicas is the number of Sentinel instances to run.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	Replicas int32 `json:"replicas,omitempty"`
}

// SentinelStatus defines the observed state of Sentinel.
type SentinelStatus struct {
	// ReadyReplicas is the number of ready Sentinel instances.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vs

// Sentinel is the Schema for the sentinels API.
type Sentinel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SentinelSpec   `json:"spec,omitempty"`
	Status SentinelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SentinelList contains a list of Sentinel.
type SentinelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sentinel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Sentinel{}, &SentinelList{})
}
