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

	namespace, ok := os.LookupEnv("NAMESPACE")
	if !ok {
		return defaultConfig, errors.New("'NAMESPACE' environment variable not found")
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
