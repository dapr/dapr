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
	HTTPPipelineSpec PipelineSpec `json:"httpPipeline,omitempty"`
	// +optional
	TracingSpec TracingSpec `json:"tracing,omitempty"`
	// +optional
	MTLSSpec MTLSSpec `json:"mtls,omitempty"`
}

// PipelineSpec defines the middleware pipeline
type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers"`
}

// HandlerSpec defines a request handlers
type HandlerSpec struct {
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	SelectorSpec SelectorSpec `json:"selector,omitempty"`
}

// MTLSSpec defines mTLS configuration
type MTLSSpec struct {
	Enabled          bool   `json:"enabled"`
	WorkloadCertTTL  string `json:"workloadCertTTL"`
	AllowedClockSkew string `json:"allowedClockSkew"`
}

// SelectorSpec selects target services to which the handler is to be applied
type SelectorSpec struct {
	Fields []SelectorField `json:"fields"`
}

// SelectorField defines a selector fields
type SelectorField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// TracingSpec is the spec object in ConfigurationSpec
type TracingSpec struct {
	SamplingRate string `json:"samplingRate"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConfigurationList is a list of Dapr event sources
type ConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Configuration `json:"items"`
}
