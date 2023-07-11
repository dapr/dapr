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

package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	env "github.com/dapr/dapr/pkg/config/env"

	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	yaml "gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/dapr/dapr/pkg/buildinfo"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/utils"
)

// Feature Flags section

type Feature string

const (
	// Enable support for streaming in HTTP service invocation
	ServiceInvocationStreaming Feature = "ServiceInvocationStreaming"
	// Disables enforcing minimum TLS version 1.2 in AppChannel, which is insecure.
	// TODO: Remove this feature flag in Dapr 1.13.
	AppChannelAllowInsecureTLS Feature = "AppChannelAllowInsecureTLS"
	// Enables support for setting TTL on Actor state keys. Remove this flag in
	// Dapr 1.12.
	ActorStateTTL Feature = "ActorStateTTL"
)

// end feature flags section

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
	AllowAccess         = "allow"
	DenyAccess          = "deny"
	DefaultTrustDomain  = "public"
	DefaultNamespace    = "default"
	ActionPolicyApp     = "app"
	ActionPolicyGlobal  = "global"
	SpiffeIDPrefix      = "spiffe://"
)

// Configuration is an internal (and duplicate) representation of Dapr's Configuration CRD.
type Configuration struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	// See https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#metadata
	metav1.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	// See https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	Spec ConfigurationSpec `json:"spec" yaml:"spec"`

	// Internal fields
	featuresEnabled map[Feature]struct{}
}

// AccessControlList is an in-memory access control list config for fast lookup.
type AccessControlList struct {
	DefaultAction string
	TrustDomain   string
	PolicySpec    map[string]AccessControlListPolicySpec
}

// AccessControlListPolicySpec is an in-memory access control list config per app for fast lookup.
type AccessControlListPolicySpec struct {
	AppName             string
	DefaultAction       string
	TrustDomain         string
	Namespace           string
	AppOperationActions *Trie
}

// AccessControlListOperationAction is an in-memory access control list config per operation for fast lookup.
type AccessControlListOperationAction struct {
	VerbAction      map[string]string
	OperationName   string
	OperationAction string
}

type ConfigurationSpec struct {
	HTTPPipelineSpec    PipelineSpec       `json:"httpPipeline,omitempty" yaml:"httpPipeline,omitempty"`
	AppHTTPPipelineSpec PipelineSpec       `json:"appHttpPipeline,omitempty" yaml:"appHttpPipeline,omitempty"`
	TracingSpec         TracingSpec        `json:"tracing,omitempty" yaml:"tracing,omitempty"`
	MTLSSpec            MTLSSpec           `json:"mtls,omitempty" yaml:"mtls,omitempty"`
	MetricSpec          MetricSpec         `json:"metric,omitempty" yaml:"metric,omitempty"`
	MetricsSpec         MetricSpec         `json:"metrics,omitempty" yaml:"metrics,omitempty"`
	Secrets             SecretsSpec        `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	AccessControlSpec   AccessControlSpec  `json:"accessControl,omitempty" yaml:"accessControl,omitempty"`
	NameResolutionSpec  NameResolutionSpec `json:"nameResolution,omitempty" yaml:"nameResolution,omitempty"`
	Features            []FeatureSpec      `json:"features,omitempty" yaml:"features,omitempty"`
	APISpec             APISpec            `json:"api,omitempty" yaml:"api,omitempty"`
	ComponentsSpec      ComponentsSpec     `json:"components,omitempty" yaml:"components,omitempty"`
	LoggingSpec         LoggingSpec        `json:"logging,omitempty" yaml:"logging,omitempty"`
}

type SecretsSpec struct {
	Scopes []SecretsScope `json:"scopes"`
}

// SecretsScope defines the scope for secrets.
type SecretsScope struct {
	DefaultAccess  string   `json:"defaultAccess,omitempty" yaml:"defaultAccess,omitempty"`
	StoreName      string   `json:"storeName" yaml:"storeName"`
	AllowedSecrets []string `json:"allowedSecrets,omitempty" yaml:"allowedSecrets,omitempty"`
	DeniedSecrets  []string `json:"deniedSecrets,omitempty" yaml:"deniedSecrets,omitempty"`
}

type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers" yaml:"handlers"`
}

// APISpec describes the configuration for Dapr APIs.
type APISpec struct {
	// List of allowed APIs. Can be used in conjunction with denied.
	Allowed APIAccessRules `json:"allowed,omitempty"`
	// List of denied APIs. Can be used in conjunction with allowed.
	Denied APIAccessRules `json:"denied,omitempty"`
}

// APIAccessRule describes an access rule for allowing a Dapr API to be enabled and accessible by an app.
type APIAccessRule struct {
	Name     string                `json:"name"`
	Version  string                `json:"version"`
	Protocol APIAccessRuleProtocol `json:"protocol"`
}

// APIAccessRules is a list of API access rules (allowlist or denylist).
type APIAccessRules []APIAccessRule

// APIAccessRuleProtocol is the type for the protocol in APIAccessRules
type APIAccessRuleProtocol string

const (
	APIAccessRuleProtocolHTTP APIAccessRuleProtocol = "http"
	APIAccessRuleProtocolGRPC APIAccessRuleProtocol = "grpc"
)

// GetRulesByProtocol returns a list of APIAccessRule objects filtered by protocol
func (r APIAccessRules) GetRulesByProtocol(protocol APIAccessRuleProtocol) []APIAccessRule {
	res := make([]APIAccessRule, len(r))
	n := 0
	for _, v := range r {
		//nolint:gocritic
		if strings.ToLower(string(v.Protocol)) == string(protocol) {
			res[n] = v
			n++
		}
	}
	return res[:n]
}

type HandlerSpec struct {
	Name         string       `json:"name" yaml:"name"`
	Type         string       `json:"type" yaml:"type"`
	Version      string       `json:"version" yaml:"version"`
	SelectorSpec SelectorSpec `json:"selector,omitempty" yaml:"selector,omitempty"`
}

// LogName returns the name of the handler that can be used in logging.
func (h HandlerSpec) LogName() string {
	return utils.ComponentLogName(h.Name, h.Type, h.Version)
}

type SelectorSpec struct {
	Fields []SelectorField `json:"fields" yaml:"fields"`
}

type SelectorField struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
}

type TracingSpec struct {
	SamplingRate string     `json:"samplingRate" yaml:"samplingRate"`
	Stdout       bool       `json:"stdout" yaml:"stdout"`
	Zipkin       ZipkinSpec `json:"zipkin" yaml:"zipkin"`
	Otel         OtelSpec   `json:"otel" yaml:"otel"`
}

// ZipkinSpec defines Zipkin exporter configurations.
type ZipkinSpec struct {
	EndpointAddress string `json:"endpointAddress" yaml:"endpointAddress"`
}

// OtelSpec defines Otel exporter configurations.
type OtelSpec struct {
	Protocol        string `json:"protocol" yaml:"protocol"`
	EndpointAddress string `json:"endpointAddress" yaml:"endpointAddress"`
	IsSecure        bool   `json:"isSecure" yaml:"isSecure"`
}

// MetricSpec configuration for metrics.
type MetricSpec struct {
	Enabled bool          `json:"enabled" yaml:"enabled"`
	Rules   []MetricsRule `json:"rules" yaml:"rules"`
}

// MetricsRule defines configuration options for a metric.
type MetricsRule struct {
	Name   string        `json:"name" yaml:"name"`
	Labels []MetricLabel `json:"labels" yaml:"labels"`
}

// MetricsLabel defines an object that allows to set regex expressions for a label.
type MetricLabel struct {
	Name  string            `json:"name" yaml:"name"`
	Regex map[string]string `json:"regex" yaml:"regex"`
}

// AppPolicySpec defines the policy data structure for each app.
type AppPolicySpec struct {
	AppName             string         `json:"appId" yaml:"appId"`
	DefaultAction       string         `json:"defaultAction" yaml:"defaultAction"`
	TrustDomain         string         `json:"trustDomain" yaml:"trustDomain"`
	Namespace           string         `json:"namespace" yaml:"namespace"`
	AppOperationActions []AppOperation `json:"operations" yaml:"operations"`
}

// AppOperation defines the data structure for each app operation.
type AppOperation struct {
	Operation string   `json:"name" yaml:"name"`
	HTTPVerb  []string `json:"httpVerb" yaml:"httpVerb"`
	Action    string   `json:"action" yaml:"action"`
}

// AccessControlSpec is the spec object in ConfigurationSpec.
type AccessControlSpec struct {
	DefaultAction string          `json:"defaultAction" yaml:"defaultAction"`
	TrustDomain   string          `json:"trustDomain" yaml:"trustDomain"`
	AppPolicies   []AppPolicySpec `json:"policies" yaml:"policies"`
}

type NameResolutionSpec struct {
	Component     string      `json:"component" yaml:"component"`
	Version       string      `json:"version" yaml:"version"`
	Configuration interface{} `json:"configuration" yaml:"configuration"`
}

type MTLSSpec struct {
	Enabled          bool   `json:"enabled" yaml:"enabled"`
	WorkloadCertTTL  string `json:"workloadCertTTL" yaml:"workloadCertTTL"`
	AllowedClockSkew string `json:"allowedClockSkew" yaml:"allowedClockSkew"`
}

// SpiffeID represents the separated fields in a spiffe id.
type SpiffeID struct {
	TrustDomain string
	Namespace   string
	AppID       string
}

// FeatureSpec defines which preview features are enabled.
type FeatureSpec struct {
	Name    Feature `json:"name" yaml:"name"`
	Enabled bool    `json:"enabled" yaml:"enabled"`
}

// ComponentsSpec describes the configuration for Dapr components
type ComponentsSpec struct {
	// Denylist of component types that cannot be instantiated
	Deny []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

// LoggingSpec defines the configuration for logging.
type LoggingSpec struct {
	// Configure API logging.
	APILogging APILoggingSpec `json:"apiLogging,omitempty" yaml:"apiLogging,omitempty"`
}

// APILoggingSpec defines the configuration for API logging.
type APILoggingSpec struct {
	// Default value for enabling API logging. Sidecars can always override this by setting `--enable-api-logging` to true or false explicitly.
	// The default value is false.
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// When enabled, obfuscates the values of URLs in HTTP API logs, logging the route name rather than the full path being invoked, which could contain PII.
	// Default: false.
	// This option has no effect if API logging is disabled.
	ObfuscateURLs bool `json:"obfuscateURLs" yaml:"obfuscateURLs"`
	// If true, health checks are not reported in API logs. Default: false.
	// This option has no effect if API logging is disabled.
	OmitHealthChecks bool `json:"omitHealthChecks,omitempty" yaml:"omitHealthChecks,omitempty"`
}

// LoadDefaultConfiguration returns the default config.
func LoadDefaultConfiguration() *Configuration {
	return &Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: TracingSpec{
				SamplingRate: "",
				Otel: OtelSpec{
					IsSecure: true,
				},
			},
			MetricSpec: MetricSpec{
				Enabled: true,
			},
			MetricsSpec: MetricSpec{
				Enabled: true,
			},
			AccessControlSpec: AccessControlSpec{
				DefaultAction: AllowAccess,
				TrustDomain:   "public",
			},
		},
	}
}

// LoadStandaloneConfiguration gets the path to a config file and loads it into a configuration.
func LoadStandaloneConfiguration(config string) (*Configuration, string, error) {
	_, err := os.Stat(config)
	if err != nil {
		return nil, "", err
	}

	b, err := os.ReadFile(config)
	if err != nil {
		return nil, "", err
	}

	// Parse environment variables from yaml
	b = []byte(os.ExpandEnv(string(b)))

	conf := LoadDefaultConfiguration()
	err = yaml.Unmarshal(b, conf)
	if err != nil {
		return nil, string(b), err
	}
	err = sortAndValidateSecretsConfiguration(conf)
	if err != nil {
		return nil, string(b), err
	}

	sortMetricsSpec(conf)
	return conf, string(b), nil
}

// LoadKubernetesConfiguration gets configuration from the Kubernetes operator with a given name.
func LoadKubernetesConfiguration(config, namespace string, podName string, operatorClient operatorv1pb.OperatorClient) (*Configuration, error) {
	resp, err := operatorClient.GetConfiguration(context.Background(), &operatorv1pb.GetConfigurationRequest{
		Name:      config,
		Namespace: namespace,
		PodName:   podName,
	}, grpcRetry.WithMax(operatorMaxRetries), grpcRetry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	if resp.GetConfiguration() == nil {
		return nil, fmt.Errorf("configuration %s not found", config)
	}
	conf := LoadDefaultConfiguration()
	err = json.Unmarshal(resp.GetConfiguration(), conf)
	if err != nil {
		return nil, err
	}

	err = sortAndValidateSecretsConfiguration(conf)
	if err != nil {
		return nil, err
	}

	sortMetricsSpec(conf)
	return conf, nil
}

// Update configuration from Otlp Environment Variables, if they exist.
func SetTracingSpecFromEnv(conf *Configuration) {
	// If Otel Endpoint is already set, then don't override.
	if conf.Spec.TracingSpec.Otel.EndpointAddress != "" {
		return
	}

	if endpoint := os.Getenv(env.OtlpExporterEndpoint); endpoint != "" {
		// remove "http://" or "https://" from the endpoint
		endpoint = strings.TrimPrefix(endpoint, "http://")
		endpoint = strings.TrimPrefix(endpoint, "https://")

		conf.Spec.TracingSpec.Otel.EndpointAddress = endpoint

		if conf.Spec.TracingSpec.SamplingRate == "" {
			conf.Spec.TracingSpec.SamplingRate = "1"
		}

		// The OTLP attribute allows 'grpc', 'http/protobuf', or 'http/json'.
		// Dapr setting can only be 'grpc' or 'http'.
		if protocol := os.Getenv(env.OtlpExporterProtocol); strings.HasPrefix(protocol, "http") {
			conf.Spec.TracingSpec.Otel.Protocol = "http"
		} else {
			conf.Spec.TracingSpec.Otel.Protocol = "grpc"
		}

		if insecure := os.Getenv(env.OtlpExporterInsecure); insecure == "true" {
			conf.Spec.TracingSpec.Otel.IsSecure = false
		}
	}
}

// Apply .metrics if set. If not, retain .metric.
func sortMetricsSpec(conf *Configuration) {
	if !conf.Spec.MetricsSpec.Enabled {
		conf.Spec.MetricSpec.Enabled = false
	}

	if len(conf.Spec.MetricsSpec.Rules) > 0 {
		conf.Spec.MetricSpec.Rules = conf.Spec.MetricsSpec.Rules
	}
}

// Validate the secrets configuration and sort to the allowed and denied lists if present.
func sortAndValidateSecretsConfiguration(conf *Configuration) error {
	scopes := conf.Spec.Secrets.Scopes
	set := sets.NewString()
	for _, scope := range scopes {
		// validate scope
		if set.Has(scope.StoreName) {
			return fmt.Errorf("%q storeName is repeated in secrets configuration", scope.StoreName)
		}
		if scope.DefaultAccess != "" &&
			!strings.EqualFold(scope.DefaultAccess, AllowAccess) &&
			!strings.EqualFold(scope.DefaultAccess, DenyAccess) {
			return fmt.Errorf("defaultAccess %q can be either allow or deny", scope.DefaultAccess)
		}
		set.Insert(scope.StoreName)

		// modify scope
		sort.Strings(scope.AllowedSecrets)
		sort.Strings(scope.DeniedSecrets)
	}

	return nil
}

// IsSecretAllowed Check if the secret is allowed to be accessed.
func (c SecretsScope) IsSecretAllowed(key string) bool {
	// By default, set allow access for the secret store.
	access := AllowAccess
	// Check and set deny access.
	if strings.EqualFold(c.DefaultAccess, DenyAccess) {
		access = DenyAccess
	}

	// If the allowedSecrets list is not empty then check if the access is specifically allowed for this key.
	if len(c.AllowedSecrets) != 0 {
		return containsKey(c.AllowedSecrets, key)
	}

	// Check key in deny list if deny list is present for the secret store.
	// If the specific key is denied, then alone deny access.
	if deny := containsKey(c.DeniedSecrets, key); deny {
		return !deny
	}

	// Check if defined default access is allow.
	return access == AllowAccess
}

// Runs Binary Search on a sorted list of strings to find a key.
func containsKey(s []string, key string) bool {
	index := sort.SearchStrings(s, key)

	return index < len(s) && s[index] == key
}

// LoadFeatures loads the list of enabled features, from the Configuration spec and from the buildinfo.
func (c *Configuration) LoadFeatures() {
	forced := buildinfo.Features()
	c.featuresEnabled = make(map[Feature]struct{}, len(c.Spec.Features)+len(forced))
	for _, feature := range c.Spec.Features {
		if feature.Name == "" || !feature.Enabled {
			continue
		}
		c.featuresEnabled[feature.Name] = struct{}{}
	}
	for _, v := range forced {
		if v == "" {
			continue
		}
		c.featuresEnabled[Feature(v)] = struct{}{}
	}
}

// IsFeatureEnabled returns true if a Feature (such as a preview) is enabled.
func (c Configuration) IsFeatureEnabled(target Feature) (enabled bool) {
	_, enabled = c.featuresEnabled[target]
	return enabled
}

// EnabledFeatures returns the list of features that have been enabled.
func (c Configuration) EnabledFeatures() []string {
	features := make([]string, len(c.featuresEnabled))
	i := 0
	for f := range c.featuresEnabled {
		features[i] = string(f)
		i++
	}
	return features[:i]
}
