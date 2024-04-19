/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"strconv"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true

// Configuration describes an Dapr configuration setting.
type Configuration struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec ConfigurationSpec `json:"spec,omitempty"`
}

// ConfigurationSpec is the spec for a configuration.
type ConfigurationSpec struct {
	// +optional
	AppHTTPPipelineSpec *PipelineSpec `json:"appHttpPipeline,omitempty"`
	// +optional
	HTTPPipelineSpec *PipelineSpec `json:"httpPipeline,omitempty"`
	// +optional
	TracingSpec *TracingSpec `json:"tracing,omitempty"`
	// +kubebuilder:default={enabled:true}
	MetricSpec *MetricSpec `json:"metric,omitempty"`
	// +kubebuilder:default={enabled:true}
	MetricsSpec *MetricSpec `json:"metrics,omitempty"`
	// +optional
	MTLSSpec *MTLSSpec `json:"mtls,omitempty"`
	// +optional
	Secrets *SecretsSpec `json:"secrets,omitempty"`
	// +optional
	AccessControlSpec *AccessControlSpec `json:"accessControl,omitempty"`
	// +optional
	NameResolutionSpec *NameResolutionSpec `json:"nameResolution,omitempty"`
	// +optional
	Features []FeatureSpec `json:"features,omitempty"`
	// +optional
	APISpec *APISpec `json:"api,omitempty"`
	// +optional
	ComponentsSpec *ComponentsSpec `json:"components,omitempty"`
	// +optional
	LoggingSpec *LoggingSpec `json:"logging,omitempty"`
	// +optional
	WasmSpec *WasmSpec `json:"wasm,omitempty"`
	// +optional
	WorkflowSpec *WorkflowSpec `json:"workflow,omitempty"`
}

// WorkflowSpec defines the configuration for Dapr workflows.
type WorkflowSpec struct {
	// maxConcurrentWorkflowInvocations is the maximum number of concurrent workflow invocations that can be scheduled by a single Dapr instance.
	// Attempted invocations beyond this will be queued until the number of concurrent invocations drops below this value.
	// If omitted, the default value of 100 will be used.
	// +optional
	MaxConcurrentWorkflowInvocations int32 `json:"maxConcurrentWorkflowInvocations,omitempty"`
	// maxConcurrentActivityInvocations is the maximum number of concurrent activities that can be processed by a single Dapr instance.
	// Attempted invocations beyond this will be queued until the number of concurrent invocations drops below this value.
	// If omitted, the default value of 100 will be used.
	// +optional
	MaxConcurrentActivityInvocations int32 `json:"maxConcurrentActivityInvocations,omitempty"`
}

// APISpec describes the configuration for Dapr APIs.
type APISpec struct {
	// List of allowed APIs. Can be used in conjunction with denied.
	// +optional
	Allowed []APIAccessRule `json:"allowed,omitempty"`
	// List of denied APIs. Can be used in conjunction with allowed.
	// +optional
	Denied []APIAccessRule `json:"denied,omitempty"`
}

// WasmSpec describes the security profile for all Dapr Wasm components.
type WasmSpec struct {
	// Force enabling strict sandbox mode for all WASM components.
	// When this is enabled, WASM components always run in strict mode regardless of their configuration.
	// Strict mode enhances security of the WASM sandbox by limiting access to certain capabilities such as real-time clocks and random number generators.
	StrictSandbox bool `json:"strictSandbox,omitempty"`
}

// APIAccessRule describes an access rule for allowing or denying a Dapr API.
type APIAccessRule struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// +optional
	Protocol string `json:"protocol,omitempty"`
}

// NameResolutionSpec is the spec for name resolution configuration.
type NameResolutionSpec struct {
	Component     string        `json:"component"`
	Version       string        `json:"version"`
	Configuration *DynamicValue `json:"configuration"`
}

// SecretsSpec is the spec for secrets configuration.
type SecretsSpec struct {
	Scopes []SecretsScope `json:"scopes"`
}

// SecretsScope defines the scope for secrets.
type SecretsScope struct {
	StoreName string `json:"storeName"`
	// +optional
	DefaultAccess string `json:"defaultAccess,omitempty"`
	// +optional
	AllowedSecrets []string `json:"allowedSecrets,omitempty"`
	// +optional
	DeniedSecrets []string `json:"deniedSecrets,omitempty"`
}

// PipelineSpec defines the middleware pipeline.
type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers"`
}

// HandlerSpec defines a request handlers.
type HandlerSpec struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// +optional
	SelectorSpec *SelectorSpec `json:"selector,omitempty"`
}

// MTLSSpec defines mTLS configuration.
type MTLSSpec struct {
	Enabled *bool `json:"enabled"`
	// +optional
	WorkloadCertTTL *string `json:"workloadCertTTL,omitempty"`
	// +optional
	AllowedClockSkew        *string `json:"allowedClockSkew,omitempty"`
	SentryAddress           string  `json:"sentryAddress"`
	ControlPlaneTrustDomain string  `json:"controlPlaneTrustDomain"`
	// Additional token validators to use.
	// When Dapr is running in Kubernetes mode, this is in addition to the built-in "kubernetes" validator.
	// In self-hosted mode, enabling a custom validator will disable the built-in "insecure" validator.
	// +optional
	TokenValidators []ValidatorSpec `json:"tokenValidators,omitempty"`
}

// GetEnabled returns true if mTLS is enabled.
func (m *MTLSSpec) GetEnabled() bool {
	// Defaults to true if unset
	return m == nil || m.Enabled == nil || *m.Enabled
}

// ValidatorSpec contains additional token validators to use.
type ValidatorSpec struct {
	// Name of the validator
	// +kubebuilder:validation:Enum={"jwks"}
	Name string `json:"name"`
	// Options for the validator, if any
	Options *DynamicValue `json:"options,omitempty"`
}

// SelectorSpec selects target services to which the handler is to be applied.
type SelectorSpec struct {
	Fields []SelectorField `json:"fields"`
}

// SelectorField defines a selector fields.
type SelectorField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// TracingSpec defines distributed tracing configuration.
type TracingSpec struct {
	SamplingRate string `json:"samplingRate"`
	// +optional
	Stdout *bool `json:"stdout,omitempty"`
	// +optional
	Zipkin *ZipkinSpec `json:"zipkin,omitempty"`
	// +optional
	Otel *OtelSpec `json:"otel,omitempty"`
}

// OtelSpec defines Otel exporter configurations.
type OtelSpec struct {
	Protocol        string `json:"protocol" yaml:"protocol"`
	EndpointAddress string `json:"endpointAddress" yaml:"endpointAddress"`
	IsSecure        *bool  `json:"isSecure" yaml:"isSecure"`
}

// ZipkinSpec defines Zipkin trace configurations.
type ZipkinSpec struct {
	EndpointAddresss string `json:"endpointAddress"`
}

// MetricSpec defines metrics configuration.
type MetricSpec struct {
	Enabled *bool `json:"enabled"`
	// +optional
	HTTP *MetricHTTP `json:"http,omitempty"`
	// +optional
	Rules []MetricsRule `json:"rules,omitempty"`
}

// MetricHTTP defines configuration for metrics for the HTTP server
type MetricHTTP struct {
	// If false (the default), metrics for the HTTP server are collected with increased cardinality.
	// +optional
	IncreasedCardinality *bool `json:"increasedCardinality,omitempty"`
}

// MetricsRule defines configuration options for a metric.
type MetricsRule struct {
	Name   string        `json:"name"`
	Labels []MetricLabel `json:"labels"`
}

// MetricsLabel defines an object that allows to set regex expressions for a label.
type MetricLabel struct {
	Name  string            `json:"name"`
	Regex map[string]string `json:"regex"`
}

// AppPolicySpec defines the policy data structure for each app.
type AppPolicySpec struct {
	AppName string `json:"appId" yaml:"appId"`
	// +optional
	DefaultAction string `json:"defaultAction,omitempty" yaml:"defaultAction,omitempty"`
	// +optional
	TrustDomain string `json:"trustDomain,omitempty" yaml:"trustDomain,omitempty"`
	// +optional
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	// +optional
	AppOperationActions []AppOperationAction `json:"operations,omitempty" yaml:"operations,omitempty"`
}

// AppOperationAction defines the data structure for each app operation.
type AppOperationAction struct {
	Operation string `json:"name" yaml:"name"`
	Action    string `json:"action" yaml:"action"`
	// +optional
	HTTPVerb []string `json:"httpVerb,omitempty" yaml:"httpVerb,omitempty"`
}

// AccessControlSpec is the spec object in ConfigurationSpec.
type AccessControlSpec struct {
	// +optional
	DefaultAction string `json:"defaultAction,omitempty" yaml:"defaultAction,omitempty"`
	// +optional
	TrustDomain string `json:"trustDomain,omitempty" yaml:"trustDomain,omitempty"`
	// +optional
	AppPolicies []AppPolicySpec `json:"policies,omitempty" yaml:"policies,omitempty"`
}

// FeatureSpec defines the features that are enabled/disabled.
type FeatureSpec struct {
	Name    string `json:"name" yaml:"name"`
	Enabled *bool  `json:"enabled" yaml:"enabled"`
}

// ComponentsSpec describes the configuration for Dapr components
type ComponentsSpec struct {
	// Denylist of component types that cannot be instantiated
	// +optional
	Deny []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

// LoggingSpec defines the configuration for logging.
type LoggingSpec struct {
	// Configure API logging.
	// +optional
	APILogging *APILoggingSpec `json:"apiLogging,omitempty" yaml:"apiLogging,omitempty"`
}

// APILoggingSpec defines the configuration for API logging.
type APILoggingSpec struct {
	// Default value for enabling API logging. Sidecars can always override this by setting `--enable-api-logging` to true or false explicitly.
	// The default value is false.
	// +optional
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// When enabled, obfuscates the values of URLs in HTTP API logs, logging the route name rather than the full path being invoked, which could contain PII.
	// Default: false.
	// This option has no effect if API logging is disabled.
	// +optional
	ObfuscateURLs *bool `json:"obfuscateURLs,omitempty" yaml:"obfuscateURLs,omitempty"`
	// If true, health checks are not reported in API logs. Default: false.
	// This option has no effect if API logging is disabled.
	// +optional
	OmitHealthChecks *bool `json:"omitHealthChecks,omitempty" yaml:"omitHealthChecks,omitempty"`
}

// +kubebuilder:object:root=true

// ConfigurationList is a list of Dapr event sources.
type ConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Configuration `json:"items"`
}

// DynamicValue is a dynamic value struct for the component.metadata pair value.
type DynamicValue struct {
	v1.JSON `json:",inline"`
}

// String returns the string representation of the raw value.
// If the value is a string, it will be unquoted as the string is guaranteed to be a JSON serialized string.
func (d *DynamicValue) String() string {
	s := string(d.Raw)
	c, err := strconv.Unquote(s)
	if err == nil {
		s = c
	}
	return s
}
