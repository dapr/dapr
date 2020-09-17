// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package config

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"strings"
	"time"

	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"
)

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
	AllowAccess         = "allow"
	DenyAccess          = "deny"
)

type Configuration struct {
	Spec ConfigurationSpec `json:"spec" yaml:"spec"`
}

type ConfigurationSpec struct {
	HTTPPipelineSpec PipelineSpec `json:"httpPipeline,omitempty" yaml:"httpPipeline,omitempty"`
	TracingSpec      TracingSpec  `json:"tracing,omitempty" yaml:"tracing,omitempty"`
	MTLSSpec         MTLSSpec     `json:"mtls,omitempty"`
	MetricSpec       MetricSpec   `json:"metric,omitempty" yaml:"metric,omitempty"`
	Secrets          SecretsSpec  `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

type SecretsSpec struct {
	// In the secret scopes list, if the storeName is repeated, the last configuration with the same storeName will
	// replace the previously specified configurations.
	Scopes []SecretsScope `json:"scopes"`
}

// SecretsScope defines the scope for secrets
type SecretsScope struct {
	DefaultAccess  string   `json:"defaultAccess,omitempty" yaml:"defaultAccess,omitempty"`
	StoreName      string   `json:"storeName" yaml:"storeName"`
	AllowedSecrets []string `json:"allowedSecrets,omitempty" yaml:"allowedSecrets,omitempty"`
	DeniedSecrets  []string `json:"deniedSecrets,omitempty" yaml:"deniedSecrets,omitempty"`
}

type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers" yaml:"handlers"`
}

type HandlerSpec struct {
	Name         string       `json:"name" yaml:"name"`
	Type         string       `json:"type" yaml:"type"`
	SelectorSpec SelectorSpec `json:"selector,omitempty" yaml:"selector,omitempty"`
}

type SelectorSpec struct {
	Fields []SelectorField `json:"fields" yaml:"fields"`
}

type SelectorField struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
}

type TracingSpec struct {
	SamplingRate string `json:"samplingRate" yaml:"samplingRate"`
	Stdout       bool   `json:"stdout" yaml:"stdout"`
}

// MetricSpec configuration for metrics
type MetricSpec struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type MTLSSpec struct {
	Enabled          bool   `json:"enabled"`
	WorkloadCertTTL  string `json:"workloadCertTTL"`
	AllowedClockSkew string `json:"allowedClockSkew"`
}

// LoadDefaultConfiguration returns the default config
func LoadDefaultConfiguration() *Configuration {
	return &Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: TracingSpec{
				SamplingRate: "",
			},
			MetricSpec: MetricSpec{
				Enabled: true,
			},
		},
	}
}

// LoadStandaloneConfiguration gets the path to a config file and loads it into a configuration
func LoadStandaloneConfiguration(config string) (*Configuration, error) {
	_, err := os.Stat(config)
	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadFile(config)
	if err != nil {
		return nil, err
	}

	var conf Configuration
	err = yaml.Unmarshal(b, &conf)
	if err != nil {
		return nil, err
	}

	return &conf, nil
}

// LoadKubernetesConfiguration gets configuration from the Kubernetes operator with a given name
func LoadKubernetesConfiguration(config, namespace string, operatorClient operatorv1pb.OperatorClient) (*Configuration, error) {
	resp, err := operatorClient.GetConfiguration(context.Background(), &operatorv1pb.GetConfigurationRequest{
		Name:      config,
		Namespace: namespace,
	}, grpc_retry.WithMax(operatorMaxRetries), grpc_retry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	if resp.GetConfiguration() == nil {
		return nil, errors.Errorf("configuration %s not found", config)
	}
	var conf Configuration
	err = json.Unmarshal(resp.GetConfiguration(), &conf)
	if err != nil {
		return nil, err
	}
	return &conf, nil
}

type ParsedSecretsConfiguration struct {
	DefaultAccess  string
	AllowedSecrets map[string]struct{}
	DeniedSecrets  map[string]struct{}
}

func (c *ParsedSecretsConfiguration) ParseSecretsDefaultAccess(input string) {
	// Accept only "allow" or "deny" strings or default to "allow".
	switch strings.ToLower(input) {
	case AllowAccess:
		c.DefaultAccess = AllowAccess
	case DenyAccess:
		c.DefaultAccess = DenyAccess
	default:
		c.DefaultAccess = AllowAccess
	}
}

func (c *ParsedSecretsConfiguration) IsSecretAllowed(key string) bool {
	// By default if the store has a record in allowedSecrets map, allow access.

	// If the allowedSecrets list is not empty then check if the access is specifically allowed for this key.
	if len(c.AllowedSecrets) != 0 {
		_, allow := c.AllowedSecrets[key]
		return allow
	}

	// If deny list is present for the secret store.
	_, deny := c.DeniedSecrets[key]
	// If the specific key is denied, then alone deny access.
	if deny {
		return !deny
	}

	return c.DefaultAccess == AllowAccess
}
