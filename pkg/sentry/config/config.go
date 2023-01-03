package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

const (
	kubernetesServiceHostEnvVar = "KUBERNETES_SERVICE_HOST"
	kubernetesConfig            = "kubernetes"
	selfHostedConfig            = "selfhosted"
	defaultPort                 = 50001
	defaultWorkloadCertTTL      = time.Hour * 24
	defaultAllowedClockSkew     = time.Minute * 15

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"
)

var log = logger.NewLogger("dapr.sentry.config")

// SentryConfig holds the configuration for the Certificate Authority.
type SentryConfig struct {
	Port             int
	TrustDomain      string
	CAStore          string
	WorkloadCertTTL  time.Duration
	AllowedClockSkew time.Duration
	RootCertPath     string
	IssuerCertPath   string
	IssuerKeyPath    string
	Features         []daprGlobalConfig.FeatureSpec
	TokenAudience    *string
}

func (c SentryConfig) GetTokenAudiences() (audiences []string) {
	if c.TokenAudience != nil && *c.TokenAudience != "" {
		audiences = strings.Split(*c.TokenAudience, ",")
	}
	return
}

var configGetters = map[string]func(string) (SentryConfig, error){
	selfHostedConfig: getSelfhostedConfig,
	kubernetesConfig: getKubernetesConfig,
}

// FromConfigName returns a Sentry configuration based on a configuration spec.
// A default configuration is loaded in case of an error.
func FromConfigName(configName string) (SentryConfig, error) {
	var confGetterFn func(string) (SentryConfig, error)

	if IsKubernetesHosted() {
		confGetterFn = configGetters[kubernetesConfig]
	} else {
		confGetterFn = configGetters[selfHostedConfig]
	}

	conf, err := confGetterFn(configName)
	if err != nil {
		err = fmt.Errorf("loading default config. couldn't find config name '%s': %w", configName, err)
		conf = getDefaultConfig()
	}

	printConfig(conf)
	return conf, err
}

func printConfig(config SentryConfig) {
	caStore := "default"
	if config.CAStore != "" {
		caStore = config.CAStore
	}

	log.Infof("configuration: [port]: %v, [ca store]: %s, [allowed clock skew]: %s, [workload cert ttl]: %s",
		config.Port, caStore, config.AllowedClockSkew.String(), config.WorkloadCertTTL.String())
}

func IsKubernetesHosted() bool {
	return os.Getenv(kubernetesServiceHostEnvVar) != ""
}

func getDefaultConfig() SentryConfig {
	return SentryConfig{
		Port:             defaultPort,
		WorkloadCertTTL:  defaultWorkloadCertTTL,
		AllowedClockSkew: defaultAllowedClockSkew,
	}
}

func getKubernetesConfig(configName string) (SentryConfig, error) {
	defaultConfig := getDefaultConfig()

	kubeConf := utils.GetConfig()
	daprClient, err := scheme.NewForConfig(kubeConf)
	if err != nil {
		return defaultConfig, err
	}

	list, err := daprClient.ConfigurationV1alpha1().Configurations(metaV1.NamespaceAll).List(metaV1.ListOptions{})
	if err != nil {
		return defaultConfig, err
	}

	if configName == "" {
		configName = defaultDaprSystemConfigName
	}

	for _, i := range list.Items {
		if i.GetName() == configName {
			spec, _ := json.Marshal(i.Spec)

			var configSpec daprGlobalConfig.ConfigurationSpec
			json.Unmarshal(spec, &configSpec)

			conf := daprGlobalConfig.Configuration{
				Spec: configSpec,
			}
			return parseConfiguration(defaultConfig, &conf)
		}
	}
	return defaultConfig, errors.New("config CRD not found")
}

func getSelfhostedConfig(configName string) (SentryConfig, error) {
	defaultConfig := getDefaultConfig()
	daprConfig, _, err := daprGlobalConfig.LoadStandaloneConfiguration(configName)
	if err != nil {
		return defaultConfig, err
	}

	if daprConfig != nil {
		return parseConfiguration(defaultConfig, daprConfig)
	}
	return defaultConfig, nil
}

func parseConfiguration(conf SentryConfig, daprConfig *daprGlobalConfig.Configuration) (SentryConfig, error) {
	if daprConfig.Spec.MTLSSpec.WorkloadCertTTL != "" {
		d, err := time.ParseDuration(daprConfig.Spec.MTLSSpec.WorkloadCertTTL)
		if err != nil {
			return conf, fmt.Errorf("error parsing WorkloadCertTTL duration: %w", err)
		}

		conf.WorkloadCertTTL = d
	}

	if daprConfig.Spec.MTLSSpec.AllowedClockSkew != "" {
		d, err := time.ParseDuration(daprConfig.Spec.MTLSSpec.AllowedClockSkew)
		if err != nil {
			return conf, fmt.Errorf("error parsing AllowedClockSkew duration: %w", err)
		}

		conf.AllowedClockSkew = d
	}

	conf.Features = daprConfig.Spec.Features

	return conf, nil
}
