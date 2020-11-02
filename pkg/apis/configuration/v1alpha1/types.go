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
	// +kubebuilder:default={enabled:true}
	MetricSpec MetricSpec `json:"metric,omitempty"`
	// +optional
	MTLSSpec MTLSSpec `json:"mtls,omitempty"`
	// +optional
	Secrets SecretsSpec `json:"secrets,omitempty"`
	// +optional
	AccessControlSpec AccessControlSpec `json:"accessControl,omitempty"`
}

// SecretsSpec is the spec for secrets configuration
type SecretsSpec struct {
	Scopes []SecretsScope `json:"scopes"`
}

// SecretsScope defines the scope for secrets
type SecretsScope struct {
	// +optional
	DefaultAccess string `json:"defaultAccess,omitempty"`
	StoreName     string `json:"storeName"`
	// +optional
	AllowedSecrets []string `json:"allowedSecrets,omitempty"`
	// +optional
	DeniedSecrets []string `json:"deniedSecrets,omitempty"`
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
	Enabled bool `json:"enabled"`
	// +optional
	WorkloadCertTTL string `json:"workloadCertTTL"`
	// +optional
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

// TracingSpec defines distributed tracing configuration
type TracingSpec struct {
	SamplingRate string `json:"samplingRate"`
}

// MetricSpec defines metrics configuration
type MetricSpec struct {
	Enabled bool `json:"enabled"`
}

// AppPolicySpec defines the policy data structure for each app
type AppPolicySpec struct {
	AppName string `json:"appId" yaml:"appId"`
	// +optional
	DefaultAction string `json:"defaultAction" yaml:"defaultAction"`
	// +optional
	TrustDomain string `json:"trustDomain" yaml:"trustDomain"`
	// +optional
	Namespace string `json:"namespace" yaml:"namespace"`
	// +optional
	AppOperationActions []AppOperationAction `json:"operations" yaml:"operations"`
}

// AppOperationAction defines the data structure for each app operation
type AppOperationAction struct {
	Operation string `json:"name" yaml:"name"`
	// +optional
	HTTPVerb []string `json:"httpVerb" yaml:"httpVerb"`
	Action   string   `json:"action" yaml:"action"`
}

// AccessControlSpec is the spec object in ConfigurationSpec
type AccessControlSpec struct {
	// +optional
	DefaultAction string `json:"defaultAction" yaml:"defaultAction"`
	// +optional
	TrustDomain string          `json:"trustDomain" yaml:"trustDomain"`
	AppPolicies []AppPolicySpec `json:"policies" yaml:"policies"`
}

// +kubebuilder:object:root=true

// ConfigurationList is a list of Dapr event sources
type ConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Configuration `json:"items"`
}
