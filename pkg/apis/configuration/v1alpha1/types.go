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
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Configuration describes an Dapr configuration setting
type Configuration struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec ConfigurationSpec `json:"spec,omitempty"`
}

// ConfigurationSpec is the spec for an configuration
type ConfigurationSpec struct {
	// +optional
	TracingSpec TracingSpec `json:"tracing,omitempty"`
}

// TracingSpec is the spec object in ConfigurationSpec
type TracingSpec struct {
	Enabled          bool   `json:"enabled"`
	ExporterType     string `json:"exporterType"`
	ExporterAddress  string `json:"exporterAddress"`
	IncludeEvent     bool   `json:"includeEvent"`
	IncludeEventBody bool   `json:"includeEventBody"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConfigurationList is a list of Dapr event sources
type ConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Configuration `json:"items"`
}
