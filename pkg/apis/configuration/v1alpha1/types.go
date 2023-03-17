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

// ConfigurationSpec is the spec for an configuration.
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
}

// APISpec describes the configuration for Dapr APIs.
type APISpec struct {
	Allowed []APIAccessRule `json:"allowed,omitempty"`
}

// APIAccessRule describes an access rule for allowing a Dapr API to be enabled and accessible by an app.
type APIAccessRule struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	// +optional
	Protocol string `json:"protocol,omitempty"`
}

// NameResolutionSpec is the spec for name resolution configuration.
type NameResolutionSpec struct {
	Component     string        `json:"component,omitempty"`
	Version       string        `json:"version,omitempty"`
	Configuration *DynamicValue `json:"configuration,omitempty"`
}

// SecretsSpec is the spec for secrets configuration.
type SecretsSpec struct {
	Scopes []SecretsScope `json:"scopes,omitempty"`
}

// SecretsScope defines the scope for secrets.
type SecretsScope struct {
	// +optional
	DefaultAccess string `json:"defaultAccess,omitempty"`
	StoreName     string `json:"storeName,omitempty"`
	// +optional
	AllowedSecrets []string `json:"allowedSecrets,omitempty"`
	// +optional
	DeniedSecrets []string `json:"deniedSecrets,omitempty"`
}

// PipelineSpec defines the middleware pipeline.
type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers,omitempty"`
}

// HandlerSpec defines a request handlers.
type HandlerSpec struct {
	Name         string        `json:"name,omitempty"`
	Type         string        `json:"type,omitempty"`
	SelectorSpec *SelectorSpec `json:"selector,omitempty"`
}

// MTLSSpec defines mTLS configuration.
type MTLSSpec struct {
	Enabled bool `json:"enabled,omitempty"`
	// +optional
	WorkloadCertTTL string `json:"workloadCertTTL,omitempty"`
	// +optional
	AllowedClockSkew string `json:"allowedClockSkew,omitempty"`
}

// SelectorSpec selects target services to which the handler is to be applied.
type SelectorSpec struct {
	Fields []SelectorField `json:"fields,omitempty"`
}

// SelectorField defines a selector fields.
type SelectorField struct {
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
}

// TracingSpec defines distributed tracing configuration.
type TracingSpec struct {
	SamplingRate string `json:"samplingRate,omitempty"`
	// +optional
	Stdout bool `json:"stdout,omitempty"`
	// +optional
	Zipkin *ZipkinSpec `json:"zipkin,omitempty"`
	// +optional
	Otel *OtelSpec `json:"otel,omitempty"`
}

// OtelSpec defines Otel exporter configurations.
type OtelSpec struct {
	Protocol        string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	EndpointAddress string `json:"endpointAddress,omitempty" yaml:"endpointAddress,omitempty"`
	IsSecure        bool   `json:"isSecure,omitempty" yaml:"isSecure,omitempty"`
}

// ZipkinSpec defines Zipkin trace configurations.
type ZipkinSpec struct {
	EndpointAddresss string `json:"endpointAddress,omitempty"`
}

// MetricSpec defines metrics configuration.
type MetricSpec struct {
	Enabled bool `json:"enabled,omitempty"`
	// +optional
	Rules []MetricsRule `json:"rules,omitempty"`
}

// MetricsRule defines configuration options for a metric.
type MetricsRule struct {
	Name   string        `json:"name,omitempty"`
	Labels []MetricLabel `json:"labels,omitempty"`
}

// MetricsLabel defines an object that allows to set regex expressions for a label.
type MetricLabel struct {
	Name  string            `json:"name,omitempty"`
	Regex map[string]string `json:"regex,omitempty"`
}

// AppPolicySpec defines the policy data structure for each app.
type AppPolicySpec struct {
	AppName string `json:"appId,omitempty" yaml:"appId,omitempty"`
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
	Operation string `json:"name,omitempty" yaml:"name,omitempty"`
	// +optional
	HTTPVerb []string `json:"httpVerb,omitempty" yaml:"httpVerb,omitempty"`
	Action   string   `json:"action,omitempty" yaml:"action,omitempty"`
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
	Enabled bool   `json:"enabled" yaml:"enabled"`
}

// ComponentsSpec describes the configuration for Dapr components
type ComponentsSpec struct {
	// Denylist of component types that cannot be instantiated
	// +optional
	Deny []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

// +kubebuilder:object:root=true

// ConfigurationList is a list of Dapr event sources.
type ConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Configuration `json:"items"`
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
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// When enabled, obfuscates the values of URLs in HTTP API logs, logging the route name rather than the full path being invoked, which could contain PII.
	// Default: false.
	// This option has no effect if API logging is disabled.
	// +optional
	ObfuscateURLs bool `json:"obfuscateURLs,omitempty" yaml:"obfuscateURLs,omitempty"`
	// If true, health checks are not reported in API logs. Default: false.
	// This option has no effect if API logging is disabled.
	// +optional
	OmitHealthChecks bool `json:"omitHealthChecks,omitempty" yaml:"omitHealthChecks,omitempty"`
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
