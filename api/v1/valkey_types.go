package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ValkeySpec defines the desired state of Valkey.
type ValkeySpec struct {
	// Replicas is the number of Valkey instances to run.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	Replicas int32 `json:"replicas,omitempty"`
}

// ValkeyStatus defines the observed state of Valkey.
type ValkeyStatus struct {
	// ReadyReplicas is the number of ready Valkey instances.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vk

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

func init() {
	SchemeBuilder.Register(&Valkey{}, &ValkeyList{})
}
