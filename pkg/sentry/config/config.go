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
	defaultWorkloadCertTTL      = time.Hour * 24
	defaultAllowedClockSkew     = time.Minute * 15

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	DefaultPort = 50001
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

// String implements fmt.Stringer.
func (c SentryConfig) String() string {
	caStore := "default"
	if c.CAStore != "" {
		caStore = c.CAStore
	}

	return fmt.Sprintf("Configuration: port:'%v' ca store:'%s', allowed clock skew:'%s', workload cert ttl:'%s'",
		c.Port, caStore, c.AllowedClockSkew.String(), c.WorkloadCertTTL.String())
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

	log.Info(conf.String())
	return conf, err
}

func IsKubernetesHosted() bool {
	return os.Getenv(kubernetesServiceHostEnvVar) != ""
}

func getDefaultConfig() SentryConfig {
	return SentryConfig{
		Port:             DefaultPort,
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
	daprConfig, err := daprGlobalConfig.LoadStandaloneConfiguration(configName)
	if err != nil {
		return defaultConfig, err
	}

	if daprConfig != nil {
		return parseConfiguration(defaultConfig, daprConfig)
	}
	return defaultConfig, nil
}

func parseConfiguration(conf SentryConfig, daprConfig *daprGlobalConfig.Configuration) (SentryConfig, error) {
	mtlsSpec := daprConfig.GetMTLSSpec()
	if mtlsSpec.WorkloadCertTTL != "" {
		d, err := time.ParseDuration(mtlsSpec.WorkloadCertTTL)
		if err != nil {
			return conf, fmt.Errorf("error parsing WorkloadCertTTL duration: %w", err)
		}

		conf.WorkloadCertTTL = d
	}

	if mtlsSpec.AllowedClockSkew != "" {
		d, err := time.ParseDuration(mtlsSpec.AllowedClockSkew)
		if err != nil {
			return conf, fmt.Errorf("error parsing AllowedClockSkew duration: %w", err)
		}

		conf.AllowedClockSkew = d
	}

	conf.Features = daprConfig.Spec.Features

	return conf, nil
}
