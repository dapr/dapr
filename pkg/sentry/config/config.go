/*
Copyright 2023 The Dapr Authors
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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/utils"
)

const (
	kubernetesServiceHostEnvVar = "KUBERNETES_SERVICE_HOST"
	kubernetesConfig            = "kubernetes"
	selfHostedConfig            = "selfhosted"
	defaultWorkloadCertTTL      = time.Hour * 24
	defaultAllowedClockSkew     = time.Minute * 15
	defaultTrustDomain          = "cluster.local"

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	DefaultPort = 50001

	// Default RootCertFilename is the filename that holds the root certificate.
	DefaultRootCertFilename = "ca.crt"

	// DefaultIssuerCertFilename is the filename that holds the issuer certificate.
	DefaultIssuerCertFilename = "issuer.crt"

	// DefaultIssuerKeyFilename is the filename that holds the issuer key.
	DefaultIssuerKeyFilename = "issuer.key"
)

// Config holds the configuration for the Certificate Authority.
type Config struct {
	Port             int
	TrustDomain      string
	CAStore          string
	WorkloadCertTTL  time.Duration
	AllowedClockSkew time.Duration
	RootCertPath     string
	IssuerCertPath   string
	IssuerKeyPath    string
	Validators       map[sentryv1pb.SignCertificateRequest_TokenValidator]map[string]string
	DefaultValidator sentryv1pb.SignCertificateRequest_TokenValidator
	Features         []daprGlobalConfig.FeatureSpec
}

// FromConfigName returns a Sentry configuration based on a configuration spec.
// A default configuration is loaded in case of an error.
func FromConfigName(configName string) (conf Config, err error) {
	if IsKubernetesHosted() {
		conf, err = getKubernetesConfig(configName)
	} else {
		conf, err = getSelfhostedConfig(configName)
	}

	if err != nil {
		err = fmt.Errorf("loading default config. couldn't find config name %q: %w", configName, err)
		conf = getDefaultConfig()
	}

	return conf, err
}

func IsKubernetesHosted() bool {
	return os.Getenv(kubernetesServiceHostEnvVar) != ""
}

func getDefaultConfig() Config {
	return Config{
		Port:             DefaultPort,
		WorkloadCertTTL:  defaultWorkloadCertTTL,
		AllowedClockSkew: defaultAllowedClockSkew,
		TrustDomain:      defaultTrustDomain,
	}
}

func getKubernetesConfig(configName string) (Config, error) {
	defaultConfig := getDefaultConfig()

	kubeConf := utils.GetConfig()
	daprClient, err := scheme.NewForConfig(kubeConf)
	if err != nil {
		return defaultConfig, err
	}

	namespace, err := security.CurrentNamespaceOrError()
	if err != nil {
		return defaultConfig, err
	}

	if configName == "" {
		configName = defaultDaprSystemConfigName
	}

	cfg, err := daprClient.ConfigurationV1alpha1().Configurations(namespace).Get(configName, metaV1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return defaultConfig, errors.New("config CRD not found")
	}

	if err != nil {
		return defaultConfig, err
	}

	spec, err := json.Marshal(cfg.Spec)
	if err != nil {
		return defaultConfig, err
	}

	var configSpec daprGlobalConfig.ConfigurationSpec
	if err := json.Unmarshal(spec, &configSpec); err != nil {
		return defaultConfig, err
	}

	return parseConfiguration(defaultConfig, &daprGlobalConfig.Configuration{
		Spec: configSpec,
	})
}

func getSelfhostedConfig(configName string) (Config, error) {
	defaultConfig := getDefaultConfig()
	daprConfig, err := daprGlobalConfig.LoadStandaloneConfiguration(configName)
	if err != nil {
		return defaultConfig, err
	}

	if daprConfig != nil {
		return parseConfiguration(defaultConfig, daprConfig)
	}
	return defaultConfig, nil
}

func parseConfiguration(conf Config, daprConfig *daprGlobalConfig.Configuration) (Config, error) {
	mtlsSpec := daprConfig.Spec.MTLSSpec
	if mtlsSpec != nil && mtlsSpec.WorkloadCertTTL != "" {
		d, err := time.ParseDuration(daprConfig.Spec.MTLSSpec.WorkloadCertTTL)
		if err != nil {
			return conf, fmt.Errorf("error parsing WorkloadCertTTL duration: %w", err)
		}

		conf.WorkloadCertTTL = d
	}

	if mtlsSpec != nil && mtlsSpec.AllowedClockSkew != "" {
		d, err := time.ParseDuration(mtlsSpec.AllowedClockSkew)
		if err != nil {
			return conf, fmt.Errorf("error parsing AllowedClockSkew duration: %w", err)
		}

		conf.AllowedClockSkew = d
	}

	if daprConfig.Spec.MTLSSpec != nil && len(daprConfig.Spec.MTLSSpec.ControlPlaneTrustDomain) > 0 {
		conf.TrustDomain = daprConfig.Spec.MTLSSpec.ControlPlaneTrustDomain
	}

	conf.Features = daprConfig.Spec.Features

	// Get token validators
	// In Kubernetes mode, we always allow the built-in "kubernetes" validator
	// In self-hosted mode, the built-in "insecure" validator is enabled only if no other validator is configured
	conf.Validators = map[sentryv1pb.SignCertificateRequest_TokenValidator]map[string]string{}
	if IsKubernetesHosted() {
		conf.DefaultValidator = sentryv1pb.SignCertificateRequest_KUBERNETES
		conf.Validators[sentryv1pb.SignCertificateRequest_KUBERNETES] = map[string]string{}
	}
	if daprConfig.Spec.MTLSSpec != nil && len(daprConfig.Spec.MTLSSpec.TokenValidators) > 0 {
		for _, v := range daprConfig.Spec.MTLSSpec.TokenValidators {
			// Check if the name is a valid one
			// We do not allow re-configuring the built-in validators
			val, ok := sentryv1pb.SignCertificateRequest_TokenValidator_value[strings.ToUpper(v.Name)]
			if !ok {
				return conf, fmt.Errorf("invalid token validator name: '%s'; supported values: 'jwks'", v.Name)
			}
			switch val {
			case int32(sentryv1pb.SignCertificateRequest_JWKS):
				// All good - nop
			case int32(sentryv1pb.SignCertificateRequest_KUBERNETES), int32(sentryv1pb.SignCertificateRequest_INSECURE):
				return conf, fmt.Errorf("invalid token validator: the built-in 'kubernetes' and 'insecure' validators cannot be configured manually")
			default:
				return conf, fmt.Errorf("invalid token validator name: '%s'; supported values: 'jwks'", v.Name)
			}

			conf.Validators[sentryv1pb.SignCertificateRequest_TokenValidator(val)] = v.OptionsMap()
		}
	} else if !IsKubernetesHosted() {
		conf.DefaultValidator = sentryv1pb.SignCertificateRequest_INSECURE
		conf.Validators[sentryv1pb.SignCertificateRequest_INSECURE] = map[string]string{}
	}

	return conf, nil
}
