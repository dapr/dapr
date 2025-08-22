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
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/spf13/cast"
	"go.opencensus.io/stats/view"
	yaml "gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/dapr/dapr/pkg/buildinfo"
	env "github.com/dapr/dapr/pkg/config/env"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

// Feature Flags section

type Feature string

const (
	// Enables support for setting TTL on Actor state keys.
	ActorStateTTL Feature = "ActorStateTTL"

	// Enables support for hot reloading of Daprd Components.
	HotReload Feature = "HotReload"

	// Enables support for using the Scheduler control plane service
	// for Actor Reminders.
	SchedulerReminders Feature = "SchedulerReminders"

	// Enables feature to support workflows in a clustered deployment.
	WorkflowsClusteredDeployment Feature = "WorkflowsClusteredDeployment"
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
)

var defaultFeatures = map[Feature]bool{
	SchedulerReminders: true,
}

// Configuration is an internal (and duplicate) representation of Dapr's Configuration CRD.
//
//nolint:recvcheck
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
	HTTPPipelineSpec    *PipelineSpec       `json:"httpPipeline,omitempty"    yaml:"httpPipeline,omitempty"`
	AppHTTPPipelineSpec *PipelineSpec       `json:"appHttpPipeline,omitempty" yaml:"appHttpPipeline,omitempty"`
	TracingSpec         *TracingSpec        `json:"tracing,omitempty"         yaml:"tracing,omitempty"`
	MTLSSpec            *MTLSSpec           `json:"mtls,omitempty"            yaml:"mtls,omitempty"`
	MetricSpec          *MetricSpec         `json:"metric,omitempty"          yaml:"metric,omitempty"`
	MetricsSpec         *MetricSpec         `json:"metrics,omitempty"         yaml:"metrics,omitempty"`
	Secrets             *SecretsSpec        `json:"secrets,omitempty"         yaml:"secrets,omitempty"`
	AccessControlSpec   *AccessControlSpec  `json:"accessControl,omitempty"   yaml:"accessControl,omitempty"`
	NameResolutionSpec  *NameResolutionSpec `json:"nameResolution,omitempty"  yaml:"nameResolution,omitempty"`
	Features            []FeatureSpec       `json:"features,omitempty"        yaml:"features,omitempty"`
	APISpec             *APISpec            `json:"api,omitempty"             yaml:"api,omitempty"`
	ComponentsSpec      *ComponentsSpec     `json:"components,omitempty"      yaml:"components,omitempty"`
	LoggingSpec         *LoggingSpec        `json:"logging,omitempty"         yaml:"logging,omitempty"`
	WasmSpec            *WasmSpec           `json:"wasm,omitempty"            yaml:"wasm,omitempty"`
	WorkflowSpec        *WorkflowSpec       `json:"workflow,omitempty"        yaml:"workflow,omitempty"`
}

// WorkflowSpec defines the configuration for Dapr workflows.
type WorkflowSpec struct {
	// maxConcurrentWorkflowInvocations is the maximum number of concurrent workflow invocations that can be scheduled by a single Dapr instance.
	// Attempted invocations beyond this will be queued until the number of concurrent invocations drops below this value.
	// If omitted, no maximum will be enforced.
	MaxConcurrentWorkflowInvocations int32 `json:"maxConcurrentWorkflowInvocations,omitempty" yaml:"maxConcurrentWorkflowInvocations,omitempty"`
	// maxConcurrentActivityInvocations is the maximum number of concurrent activities that can be processed by a single Dapr instance.
	// Attempted invocations beyond this will be queued until the number of concurrent invocations drops below this value.
	// If omitted, no maximum will be enforced.
	MaxConcurrentActivityInvocations int32 `json:"maxConcurrentActivityInvocations,omitempty" yaml:"maxConcurrentActivityInvocations,omitempty"`
}

func (w *WorkflowSpec) GetMaxConcurrentWorkflowInvocations() *int32 {
	if w == nil || w.MaxConcurrentWorkflowInvocations <= 0 {
		return nil
	}
	return ptr.Of(w.MaxConcurrentWorkflowInvocations)
}

func (w *WorkflowSpec) GetMaxConcurrentActivityInvocations() *int32 {
	if w == nil || w.MaxConcurrentActivityInvocations <= 0 {
		return nil
	}
	return ptr.Of(w.MaxConcurrentActivityInvocations)
}

type SecretsSpec struct {
	Scopes []SecretsScope `json:"scopes,omitempty"`
}

// SecretsScope defines the scope for secrets.
type SecretsScope struct {
	DefaultAccess  string   `json:"defaultAccess,omitempty"  yaml:"defaultAccess,omitempty"`
	StoreName      string   `json:"storeName,omitempty"      yaml:"storeName,omitempty"`
	AllowedSecrets []string `json:"allowedSecrets,omitempty" yaml:"allowedSecrets,omitempty"`
	DeniedSecrets  []string `json:"deniedSecrets,omitempty"  yaml:"deniedSecrets,omitempty"`
}

type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers,omitempty" yaml:"handlers,omitempty"`
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

// GetRulesByProtocol returns a list of APIAccessRule objects for a protocol
// The result is a map where the key is in the format "<version>/<endpoint>"
func (r APIAccessRules) GetRulesByProtocol(protocol APIAccessRuleProtocol) map[string]struct{} {
	res := make(map[string]struct{}, len(r))
	for _, v := range r {
		//nolint:gocritic
		if strings.ToLower(string(v.Protocol)) == string(protocol) {
			key := v.Version + "/" + v.Name
			res[key] = struct{}{}
		}
	}
	return res
}

type HandlerSpec struct {
	Name         string       `json:"name,omitempty"     yaml:"name,omitempty"`
	Type         string       `json:"type,omitempty"     yaml:"type,omitempty"`
	Version      string       `json:"version,omitempty"  yaml:"version,omitempty"`
	SelectorSpec SelectorSpec `json:"selector,omitempty" yaml:"selector,omitempty"`
}

// LogName returns the name of the handler that can be used in logging.
func (h HandlerSpec) LogName() string {
	return utils.ComponentLogName(h.Name, h.Type, h.Version)
}

type SelectorSpec struct {
	Fields []SelectorField `json:"fields,omitempty" yaml:"fields,omitempty"`
}

type SelectorField struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
}

type TracingSpec struct {
	SamplingRate string      `json:"samplingRate,omitempty" yaml:"samplingRate,omitempty"`
	Stdout       bool        `json:"stdout,omitempty" yaml:"stdout,omitempty"`
	Zipkin       *ZipkinSpec `json:"zipkin,omitempty" yaml:"zipkin,omitempty"`
	Otel         *OtelSpec   `json:"otel,omitempty" yaml:"otel,omitempty"`
}

// ZipkinSpec defines Zipkin exporter configurations.
type ZipkinSpec struct {
	EndpointAddress string `json:"endpointAddress,omitempty" yaml:"endpointAddress,omitempty"`
}

// OtelSpec defines Otel exporter configurations.
type OtelSpec struct {
	Protocol        string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	EndpointAddress string `json:"endpointAddress,omitempty" yaml:"endpointAddress,omitempty"`
	// Defaults to true
	IsSecure *bool `json:"isSecure,omitempty" yaml:"isSecure,omitempty"`
	// Headers to add to the request
	Headers string `json:"headers,omitempty" yaml:"headers,omitempty"`
	// Timeout for the request in milliseconds
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"` // Defaults to 10000
}

// GetIsSecure returns true if the connection should be secured.
func (o OtelSpec) GetIsSecure() bool {
	// Defaults to true if nil
	return o.IsSecure == nil || *o.IsSecure
}

// MetricSpec configuration for metrics.
type MetricSpec struct {
	// Defaults to true
	Enabled          *bool       `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	RecordErrorCodes *bool       `json:"recordErrorCodes,omitempty"  yaml:"recordErrorCodes,omitempty"`
	HTTP             *MetricHTTP `json:"http,omitempty" yaml:"http,omitempty"`
	// Latency distribution buckets. If not set, the default buckets are used.
	LatencyDistributionBuckets *[]int        `json:"latencyDistributionBuckets,omitempty" yaml:"latencyDistributionBuckets,omitempty"`
	Rules                      []MetricsRule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// GetEnabled returns true if metrics are enabled.
func (m MetricSpec) GetEnabled() bool {
	// Defaults to true if nil
	return m.Enabled == nil || *m.Enabled
}

// GetHTTPIncreasedCardinality returns true if increased cardinality is enabled for HTTP metrics
func (m MetricSpec) GetHTTPIncreasedCardinality(log logger.Logger) bool {
	if m.HTTP == nil || m.HTTP.IncreasedCardinality == nil {
		// The default is true in Dapr 1.13, but will be changed to false in 1.15+
		// TODO: [MetricsCardinality] Change default in 1.15+
		log.Warn("The default value for 'spec.metric.http.increasedCardinality' will change to 'false' in Dapr 1.15 or later")
		return true
	}
	return *m.HTTP.IncreasedCardinality
}

// GetLatencyDistribution returns a *view.Aggregration to be used for latency histograms
func (m MetricSpec) GetLatencyDistribution(log logger.Logger) *view.Aggregation {
	defaultLatencyDistribution := []float64{1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1_000, 2_000, 5_000, 10_000, 20_000, 50_000, 100_000}
	metricSpecBytes, err := json.Marshal(m)
	if err != nil {
		log.Errorf("Error marshalling metric spec to JSON: %s", err)
	}
	log.Infof("metric spec: %s", string(metricSpecBytes))
	if m.LatencyDistributionBuckets == nil || len(*m.LatencyDistributionBuckets) == 0 {
		// The default is defaultLatencyDistribution
		log.Infof("Using default latency distribution buckets: %v", defaultLatencyDistribution)
		return view.Distribution(defaultLatencyDistribution...)
	}
	log.Infof("Using custom latency distribution buckets: %v", *m.LatencyDistributionBuckets)
	buckets := make([]float64, len(*m.LatencyDistributionBuckets))
	for i, v := range *m.LatencyDistributionBuckets {
		buckets[i] = float64(v)
	}

	return view.Distribution(buckets...)
}

// GetHTTPExcludeVerbs returns true if exclude verbs is enabled for HTTP metrics
func (m MetricSpec) GetHTTPExcludeVerbs() bool {
	if m.HTTP == nil || m.HTTP.ExcludeVerbs == nil {
		// The default is false
		return false
	}
	return *m.HTTP.ExcludeVerbs
}

// GetHTTPPathMatching returns the path matching configuration for HTTP metrics
func (m MetricSpec) GetHTTPPathMatching() []string {
	if m.HTTP == nil {
		return nil
	}
	return m.HTTP.PathMatching
}

// GetRecordErrorCodes returns true if `recordErrorCodes` is enabled for metrics
func (m MetricSpec) GetRecordErrorCodes() bool {
	if m.RecordErrorCodes == nil {
		// The default is false
		return false
	}
	return *m.RecordErrorCodes
}

// MetricHTTP defines configuration for metrics for the HTTP server
type MetricHTTP struct {
	// If false, metrics for the HTTP server are collected with increased cardinality.
	// The default is true in Dapr 1.13, but will be changed to false in 1.15+
	// TODO: [MetricsCardinality] Change default in 1.15+
	// +optional
	IncreasedCardinality *bool `json:"increasedCardinality,omitempty" yaml:"increasedCardinality,omitempty"`
	// +optional
	PathMatching []string `json:"pathMatching,omitempty" yaml:"pathMatching,omitempty"`
	// If true (default is false) HTTP verbs (e.g., GET, POST) are excluded from the metrics.
	// +optional
	ExcludeVerbs *bool `json:"excludeVerbs,omitempty" yaml:"excludeVerbs,omitempty"`
}

// MetricsRule defines configuration options for a metric.
type MetricsRule struct {
	Name   string        `json:"name,omitempty"   yaml:"name,omitempty"`
	Labels []MetricLabel `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// MetricsLabel defines an object that allows to set regex expressions for a label.
type MetricLabel struct {
	Name  string            `json:"name,omitempty"  yaml:"name,omitempty"`
	Regex map[string]string `json:"regex,omitempty" yaml:"regex,omitempty"`
}

// AppPolicySpec defines the policy data structure for each app.
type AppPolicySpec struct {
	AppName             string         `json:"appId,omitempty" yaml:"appId,omitempty"`
	DefaultAction       string         `json:"defaultAction,omitempty" yaml:"defaultAction,omitempty"`
	TrustDomain         string         `json:"trustDomain,omitempty" yaml:"trustDomain,omitempty"`
	Namespace           string         `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	AppOperationActions []AppOperation `json:"operations,omitempty" yaml:"operations,omitempty"`
}

// AppOperation defines the data structure for each app operation.
type AppOperation struct {
	Operation string   `json:"name,omitempty" yaml:"name,omitempty"`
	HTTPVerb  []string `json:"httpVerb,omitempty" yaml:"httpVerb,omitempty"`
	Action    string   `json:"action,omitempty" yaml:"action,omitempty"`
}

// AccessControlSpec is the spec object in ConfigurationSpec.
type AccessControlSpec struct {
	DefaultAction string          `json:"defaultAction,omitempty" yaml:"defaultAction,omitempty"`
	TrustDomain   string          `json:"trustDomain,omitempty"   yaml:"trustDomain,omitempty"`
	AppPolicies   []AppPolicySpec `json:"policies,omitempty"      yaml:"policies,omitempty"`
}

type NameResolutionSpec struct {
	Component     string `json:"component,omitempty"     yaml:"component,omitempty"`
	Version       string `json:"version,omitempty"       yaml:"version,omitempty"`
	Configuration any    `json:"configuration,omitempty" yaml:"configuration,omitempty"`
}

// MTLSSpec defines mTLS configuration.
type MTLSSpec struct {
	Enabled                 bool   `json:"enabled,omitempty"                 yaml:"enabled,omitempty"`
	WorkloadCertTTL         string `json:"workloadCertTTL,omitempty"         yaml:"workloadCertTTL,omitempty"`
	AllowedClockSkew        string `json:"allowedClockSkew,omitempty"        yaml:"allowedClockSkew,omitempty"`
	SentryAddress           string `json:"sentryAddress,omitempty"           yaml:"sentryAddress,omitempty"`
	ControlPlaneTrustDomain string `json:"controlPlaneTrustDomain,omitempty" yaml:"controlPlaneTrustDomain,omitempty"`
	// Additional token validators to use.
	// When Dapr is running in Kubernetes mode, this is in addition to the built-in "kubernetes" validator.
	// In self-hosted mode, enabling a custom validator will disable the built-in "insecure" validator.
	TokenValidators []ValidatorSpec `json:"tokenValidators,omitempty" yaml:"tokenValidators,omitempty"`
}

// ValidatorSpec contains additional token validators to use.
type ValidatorSpec struct {
	// Name of the validator
	Name string `json:"name"`
	// Options for the validator, if any
	Options any `json:"options,omitempty"`
}

// OptionsMap returns the validator options as a map[string]string.
// If the options are empty, or if the conversion fails, returns nil.
func (v ValidatorSpec) OptionsMap() map[string]string {
	if v.Options == nil {
		return nil
	}

	return cast.ToStringMapString(v.Options)
}

// FeatureSpec defines which preview features are enabled.
type FeatureSpec struct {
	Name    Feature `json:"name"    yaml:"name"`
	Enabled bool    `json:"enabled" yaml:"enabled"`
}

// ComponentsSpec describes the configuration for Dapr components
type ComponentsSpec struct {
	// Denylist of component types that cannot be instantiated
	Deny []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

// WasmSpec describes the security profile for all Dapr Wasm components.
type WasmSpec struct {
	// Force enabling strict sandbox mode for all WASM components.
	// When this is enabled, WASM components always run in strict mode regardless of their configuration.
	// Strict mode enhances security of the WASM sandbox by limiting access to certain capabilities such as real-time clocks and random number generators.
	StrictSandbox bool `json:"strictSandbox,omitempty" yaml:"strictSandbox,omitempty"`
}

// GetStrictSandbox returns the value of StrictSandbox, with nil-checks.
func (w *WasmSpec) GetStrictSandbox() bool {
	return w != nil && w.StrictSandbox
}

// LoggingSpec defines the configuration for logging.
type LoggingSpec struct {
	// Configure API logging.
	APILogging *APILoggingSpec `json:"apiLogging,omitempty" yaml:"apiLogging,omitempty"`
}

// APILoggingSpec defines the configuration for API logging.
type APILoggingSpec struct {
	// Default value for enabling API logging. Sidecars can always override this by setting `--enable-api-logging` to true or false explicitly.
	// The default value is false.
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// When enabled, obfuscates the values of URLs in HTTP API logs, logging the route name rather than the full path being invoked, which could contain PII.
	// Default: false.
	// This option has no effect if API logging is disabled.
	ObfuscateURLs bool `json:"obfuscateURLs,omitempty" yaml:"obfuscateURLs,omitempty"`
	// If true, health checks are not reported in API logs. Default: false.
	// This option has no effect if API logging is disabled.
	OmitHealthChecks bool `json:"omitHealthChecks,omitempty" yaml:"omitHealthChecks,omitempty"`
}

// LoadDefaultConfiguration returns the default config.
func LoadDefaultConfiguration() *Configuration {
	return &Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: &TracingSpec{
				Otel: &OtelSpec{
					IsSecure: ptr.Of(true),
				},
			},
			MetricSpec: &MetricSpec{
				Enabled: ptr.Of(true),
			},
			AccessControlSpec: &AccessControlSpec{
				DefaultAction: AllowAccess,
				TrustDomain:   "public",
			},
		},
	}
}

// LoadStandaloneConfiguration gets the path to a config file and loads it into a configuration.
func LoadStandaloneConfiguration(configs ...string) (*Configuration, error) {
	conf := LoadDefaultConfiguration()

	// Load all config files and apply them on top of the default config
	for _, config := range configs {
		_, err := os.Stat(config)
		if err != nil {
			return nil, err
		}

		b, err := os.ReadFile(config)
		if err != nil {
			return nil, err
		}

		// Parse environment variables from yaml
		b = []byte(os.ExpandEnv(string(b)))

		err = yaml.Unmarshal(b, conf)
		if err != nil {
			return nil, err
		}
	}

	err := conf.sortAndValidateSecretsConfiguration()
	if err != nil {
		return nil, err
	}

	conf.sortMetricsSpec()
	conf.SetDefaultFeatures()
	return conf, nil
}

// LoadKubernetesConfiguration gets configuration from the Kubernetes operator with a given name.
func LoadKubernetesConfiguration(config string, namespace string, podName string, operatorClient operatorv1pb.OperatorClient) (*Configuration, error) {
	resp, err := operatorClient.GetConfiguration(context.Background(), &operatorv1pb.GetConfigurationRequest{
		Name:      config,
		Namespace: namespace,
		PodName:   podName,
	}, grpcRetry.WithMax(operatorMaxRetries), grpcRetry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	b := resp.GetConfiguration()
	if len(b) == 0 {
		return nil, fmt.Errorf("configuration %s not found", config)
	}
	conf := LoadDefaultConfiguration()
	err = json.Unmarshal(b, conf)
	if err != nil {
		return nil, err
	}

	err = conf.sortAndValidateSecretsConfiguration()
	if err != nil {
		return nil, err
	}

	conf.sortMetricsSpec()
	conf.SetDefaultFeatures()
	return conf, nil
}

// Update configuration from Otlp Environment Variables, if they exist.
func SetTracingSpecFromEnv(conf *Configuration) error {
	// If Otel Endpoint is already set, then don't override.
	if conf.Spec.TracingSpec.Otel.EndpointAddress != "" {
		return nil
	}

	var endpoint string
	if ep := os.Getenv(env.OtlpExporterTracesEndpoint); ep != "" {
		endpoint = ep
	} else if ep := os.Getenv(env.OtlpExporterEndpoint); ep != "" {
		endpoint = ep
	}

	if endpoint != "" {
		// remove "http://" or "https://" from the endpoint
		endpoint = strings.TrimPrefix(endpoint, "http://")
		endpoint = strings.TrimPrefix(endpoint, "https://")

		conf.Spec.TracingSpec.Otel.EndpointAddress = endpoint

		if conf.Spec.TracingSpec.SamplingRate == "" {
			conf.Spec.TracingSpec.SamplingRate = "1"
		}

		// The OTLP attribute allows 'grpc', 'http/protobuf', or 'http/json'.
		// Dapr setting can only be 'grpc' or 'http'.
		var protocol string
		if p := os.Getenv(env.OtlpExporterTracesProtocol); p != "" {
			protocol = p
		} else if p := os.Getenv(env.OtlpExporterProtocol); p != "" {
			protocol = p
		}

		if strings.HasPrefix(protocol, "http") {
			conf.Spec.TracingSpec.Otel.Protocol = "http"
		} else {
			conf.Spec.TracingSpec.Otel.Protocol = "grpc"
		}

		if insecure := os.Getenv(env.OtlpExporterInsecure); insecure == "true" {
			conf.Spec.TracingSpec.Otel.IsSecure = ptr.Of(false)
		}
	}

	var headers string
	if h := os.Getenv(env.OtlpExporterTracesHeaders); h != "" {
		headers = h
	} else if h := os.Getenv(env.OtlpExporterHeaders); h != "" {
		headers = h
	}

	if headers != "" {
		conf.Spec.TracingSpec.Otel.Headers = headers
	}

	var timeoutMs int
	if t := os.Getenv(env.OtlpExporterTracesTimeout); t != "" {
		ti, err := strconv.Atoi(t)
		if err != nil {
			return err
		}
		timeoutMs = ti
	} else if t := os.Getenv(env.OtlpExporterTimeout); t != "" {
		ti, err := strconv.Atoi(t)
		if err != nil {
			return err
		}
		timeoutMs = ti
	}

	if timeoutMs > 0 {
		conf.Spec.TracingSpec.Otel.Timeout = timeoutMs
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
		if feature.Name == "" {
			continue
		}

		// If the feature is disabled in configuration, remove it from the list of default enabled features if it exists
		if !feature.Enabled {
			_, exists := c.featuresEnabled[feature.Name]
			if exists {
				delete(c.featuresEnabled, feature.Name)
			}
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

// GetTracingSpec returns the tracing spec.
// It's a short-hand that includes nil-checks for safety.
func (c Configuration) GetTracingSpec() TracingSpec {
	if c.Spec.TracingSpec == nil {
		return TracingSpec{}
	}
	return *c.Spec.TracingSpec
}

// GetMTLSSpec returns the mTLS spec.
// It's a short-hand that includes nil-checks for safety.
func (c Configuration) GetMTLSSpec() MTLSSpec {
	if c.Spec.MTLSSpec == nil {
		return MTLSSpec{}
	}
	return *c.Spec.MTLSSpec
}

// GetMetricsSpec returns the metrics spec.
// It's a short-hand that includes nil-checks for safety.
func (c Configuration) GetMetricsSpec() MetricSpec {
	if c.Spec.MetricSpec == nil {
		return MetricSpec{}
	}
	return *c.Spec.MetricSpec
}

// GetAPISpec returns the API spec.
// It's a short-hand that includes nil-checks for safety.
func (c Configuration) GetAPISpec() APISpec {
	if c.Spec.APISpec == nil {
		return APISpec{}
	}
	return *c.Spec.APISpec
}

// GetLoggingSpec returns the Logging spec.
// It's a short-hand that includes nil-checks for safety.
func (c Configuration) GetLoggingSpec() LoggingSpec {
	if c.Spec.LoggingSpec == nil {
		return LoggingSpec{}
	}
	return *c.Spec.LoggingSpec
}

// GetAPILoggingSpec returns the Logging.APILogging spec.
// It's a short-hand that includes nil-checks for safety.
func (c Configuration) GetAPILoggingSpec() APILoggingSpec {
	if c.Spec.LoggingSpec == nil || c.Spec.LoggingSpec.APILogging == nil {
		return APILoggingSpec{}
	}
	return *c.Spec.LoggingSpec.APILogging
}

// ToYAML returns the Configuration represented as YAML.
func (c *Configuration) ToYAML() (string, error) {
	b, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// String implements fmt.Stringer and is used for debugging. It returns the Configuration object encoded as YAML.
func (c *Configuration) String() string {
	enc, err := c.ToYAML()
	if err != nil {
		return "Failed to marshal Configuration object to YAML: " + err.Error()
	}
	return enc
}

// Apply .metrics if set. If not, retain .metric.
func (c *Configuration) sortMetricsSpec() {
	if c.Spec.MetricsSpec == nil {
		return
	}

	if c.Spec.MetricsSpec.Enabled != nil {
		c.Spec.MetricSpec.Enabled = c.Spec.MetricsSpec.Enabled
	}

	if len(c.Spec.MetricsSpec.Rules) > 0 {
		c.Spec.MetricSpec.Rules = c.Spec.MetricsSpec.Rules
	}

	if c.Spec.MetricsSpec.HTTP != nil {
		c.Spec.MetricSpec.HTTP = c.Spec.MetricsSpec.HTTP
	}

	if c.Spec.MetricsSpec.LatencyDistributionBuckets != nil {
		c.Spec.MetricSpec.LatencyDistributionBuckets = c.Spec.MetricsSpec.LatencyDistributionBuckets
	}

	if c.Spec.MetricsSpec.RecordErrorCodes != nil {
		c.Spec.MetricSpec.RecordErrorCodes = c.Spec.MetricsSpec.RecordErrorCodes
	}
}

// Validate the secrets configuration and sort to the allowed and denied lists if present.
func (c *Configuration) sortAndValidateSecretsConfiguration() error {
	if c.Spec.Secrets == nil {
		return nil
	}

	set := sets.NewString()
	for _, scope := range c.Spec.Secrets.Scopes {
		// validate scope
		if set.Has(scope.StoreName) {
			return fmt.Errorf("%s storeName is repeated in secrets configuration", scope.StoreName)
		}
		if scope.DefaultAccess != "" &&
			!strings.EqualFold(scope.DefaultAccess, AllowAccess) &&
			!strings.EqualFold(scope.DefaultAccess, DenyAccess) {
			return fmt.Errorf("defaultAccess %s can be either allow or deny", scope.DefaultAccess)
		}
		set.Insert(scope.StoreName)

		// modify scope
		sort.Strings(scope.AllowedSecrets)
		sort.Strings(scope.DeniedSecrets)
	}

	return nil
}

func (c *Configuration) SetDefaultFeatures() {
	if c.Spec.Features == nil {
		c.Spec.Features = make([]FeatureSpec, 0)
	}

	for feature, enabled := range defaultFeatures {
		featureSpecExists := false
		for _, featureSpec := range c.Spec.Features {
			if featureSpec.Name == feature {
				featureSpecExists = true
				break
			}
		}
		if !featureSpecExists {
			c.Spec.Features = append(c.Spec.Features, FeatureSpec{
				Name:    feature,
				Enabled: enabled,
			})
		}
	}
}

// ToYAML returns the ConfigurationSpec represented as YAML.
func (c ConfigurationSpec) ToYAML() (string, error) {
	b, err := yaml.Marshal(&c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// String implements fmt.Stringer and is used for debugging. It returns the Configuration object encoded as YAML.
func (c ConfigurationSpec) String() string {
	enc, err := c.ToYAML()
	if err != nil {
		return fmt.Sprintf("Failed to marshal ConfigurationSpec object to YAML: %v", err)
	}
	return enc
}

// StringToHeader converting a string to a map of headers.
// example: header1=value1,header2=value2
// For the exact consistent experience of parsing configuration values
// and due to the function being in the internal package, we cannot use it directly.
// The function was copied and adapted from OpenTelemetry:
// https://github.com/open-telemetry/opentelemetry-go/blob/f4e20525957b6b8177175c57bd4ede5ba591f716/internal/shared/otlp/envconfig/envconfig.go.tmpl#L157
// modified to return an error if the header is not valid.
func StringToHeader(value string) (map[string]string, error) {
	headersPairs := strings.Split(value, ",")
	headers := make(map[string]string)

	for _, header := range headersPairs {
		n, v, found := strings.Cut(header, "=")
		if !found {
			return nil, errors.New("missing '=' parse headers input" + header)
		}

		trimmedName := strings.TrimSpace(n)

		// Validate the key.
		if !isValidHeaderKey(trimmedName) {
			return nil, errors.New("invalid header key" + trimmedName)
		}

		// Only decode the value.
		value, err := url.PathUnescape(v)
		if err != nil {
			return nil, errors.Join(err, errors.New("escape header value"+v))
		}
		trimmedValue := strings.TrimSpace(value)

		headers[trimmedName] = trimmedValue
	}

	return headers, nil
}

// For the exact consistent experience of parsing configuration values
// and due to the function being in the internal package, we cannot use it directly.
// The function was copied from OpenTelemetry:
// https://github.com/open-telemetry/opentelemetry-go/blob/f4e20525957b6b8177175c57bd4ede5ba591f716/internal/shared/otlp/envconfig/envconfig.go.tmpl#L198
func isValidHeaderKey(key string) bool {
	if key == "" {
		return false
	}
	for _, c := range key {
		if !isTokenChar(c) {
			return false
		}
	}
	return true
}

// For the exact consistent experience of parsing configuration values
// and due to the function being in the internal package, we cannot use it directly.
// The function was copied from OpenTelemetry:
// https://github.com/open-telemetry/opentelemetry-go/blob/f4e20525957b6b8177175c57bd4ede5ba591f716/internal/shared/otlp/envconfig/envconfig.go.tmpl#L210C1-L215C2
func isTokenChar(c rune) bool {
	return c <= unicode.MaxASCII && (unicode.IsLetter(c) ||
		unicode.IsDigit(c) ||
		c == '!' || c == '#' || c == '$' || c == '%' || c == '&' || c == '\'' || c == '*' ||
		c == '+' || c == '-' || c == '.' || c == '^' || c == '_' || c == '`' || c == '|' || c == '~')
}
