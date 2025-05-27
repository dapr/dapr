package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// Replicator describes a Dapr state replication component configuration.
type Replicator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"` // Name of the component
	Spec              ReplicatorSpec              `json:"spec,omitempty"`      // Specification of the component
	Scopes            []string                    `json:"scopes,omitempty"`    // Scopes that this component applies to
	Auth              *Auth                       `json:"auth,omitempty"`      // Auth block for secrets
}

// ReplicatorSpec is the spec for a state replication component.
type ReplicatorSpec struct {
	Type     string            `json:"type"`     // Type of the replicator component (e.g., "replicator.redis-kafka")
	Version  string            `json:"version"`  // Version of the replicator component
	Metadata map[string]string `json:"metadata"` // Metadata for the component
}

// +kubebuilder:object:root=true

// ReplicatorList is a list of Replicator resources.
type ReplicatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Replicator `json:"items"`
}

// Needed for CRD generation, even if empty for now
func init() {
	SchemeBuilder.Register(&Replicator{}, &ReplicatorList{})
}
