package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	dapr_config "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/utils"
	log "github.com/sirupsen/logrus"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RootCertFilename            = "trust.pem"
	IssuerCertFilename          = "crt.pem"
	IssuerKeyFilename           = "key.pem"
	kubernetesServiceHostEnvVar = "KUBERNETES_SERVICE_HOST"
	kubernetesConfig            = "kubernetes"
	selfHostedConfig            = "selfhosted"
	defaultPort                 = 50001
	defaultWorkloadCertTTL      = time.Hour * 24
	defaultAllowedClockSkew     = time.Second * 20
	defaultConfigName           = "mtls"
)

// SentryConfig holds the configuration for the Certificate Authority.
type SentryConfig struct {
	Port             int
	CAStore          string
	WorkloadCertTTL  time.Duration
	AllowedClockSkew time.Duration
	RootCertPath     string
	IssuerCertPath   string
	IssuerKeyPath    string
}

var configGetters = map[string]func(string) (SentryConfig, error){
	selfHostedConfig: getSelfhostedConfig,
	kubernetesConfig: getKubernetesConfig,
}

// FromConfigName returns a Sentry configuration based on a configuration spec.
// A default configuration is loaded in case of an error.
func FromConfigName(configName string) (SentryConfig, error) {
	var confGetterFn func(string) (SentryConfig, error)

	if isKubernetesHosted() {
		confGetterFn = configGetters[kubernetesConfig]
	} else {
		confGetterFn = configGetters[selfHostedConfig]
	}

	conf, err := confGetterFn(configName)
	if err != nil {
		err = fmt.Errorf("error getting config: %s. loading default.", err)
		conf = getDefaultConfig()
	}

	printConfig(conf)
	return conf, err
}

func printConfig(config SentryConfig) {
	log.Infof("config settings - port: %v, ca store: %s, allowed CA clock skew: %s, workloadCertTTL: %s",
		config.Port, config.CAStore, config.AllowedClockSkew.String(), config.WorkloadCertTTL.String())
}

func isKubernetesHosted() bool {
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

	list, err := daprClient.ConfigurationV1alpha1().Configurations(meta_v1.NamespaceAll).List(meta_v1.ListOptions{})
	if err != nil {
		return defaultConfig, err
	}

	if configName == "" {
		configName = defaultConfigName
	}

	for _, i := range list.Items {
		if i.GetName() == configName {
			spec, _ := json.Marshal(i.Spec)

			var configSpec dapr_config.ConfigurationSpec
			json.Unmarshal(spec, &configSpec)

			conf := dapr_config.Configuration{
				Spec: configSpec,
			}
			return parseConfiguration(defaultConfig, &conf)
		}
	}
	return defaultConfig, errors.New("config CRD not found")
}

func getSelfhostedConfig(configName string) (SentryConfig, error) {
	defaultConfig := getDefaultConfig()
	daprConfig, err := dapr_config.LoadStandaloneConfiguration(configName)
	if err != nil {
		return defaultConfig, err
	}

	if daprConfig != nil {
		return parseConfiguration(defaultConfig, daprConfig)
	}
	return defaultConfig, nil
}

func parseConfiguration(conf SentryConfig, daprConfig *dapr_config.Configuration) (SentryConfig, error) {
	if daprConfig.Spec.MTLSSpec.WorkloadCertTTL != "" {
		d, err := time.ParseDuration(daprConfig.Spec.MTLSSpec.WorkloadCertTTL)
		if err != nil {
			return conf, fmt.Errorf("error parsing workloadCertTTL duration: %s", err)
		}

		conf.WorkloadCertTTL = d
	}
	return conf, nil
}
