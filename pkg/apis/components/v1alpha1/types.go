// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true

// Component describes an Dapr component type
type Component struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec ComponentSpec `json:"spec,omitempty"`
	// +optional
	Auth `json:"auth,omitempty"`
	// +optional
	Scopes []string `json:"scopes,omitempty"`
}

// ComponentSpec is the spec for a component
type ComponentSpec struct {
	Type     string         `json:"type"`
	Version  string         `json:"version"`
	Metadata []MetadataItem `json:"metadata"`
}

// MetadataItem is a name/value pair for a metadata
type MetadataItem struct {
	Name         string       `json:"name"`
	Value        string       `json:"value"`
	SecretKeyRef SecretKeyRef `json:"secretKeyRef,omitempty"`
}

// SecretKeyRef is a reference to a secret holding the value for the metadata item. Name is the secret name, and key is the field in the secret.
type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// Auth represents authentication details for the component
type Auth struct {
	SecretStore string `json:"secretStore"`
}

// +kubebuilder:object:root=true

// ComponentList is a list of Dapr components
type ComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Component `json:"items"`
}
