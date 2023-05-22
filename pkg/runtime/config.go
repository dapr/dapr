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

package runtime

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

<<<<<<< HEAD
	"github.com/dapr/dapr/pkg/config"
	modesConfig "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/config/protocol"
=======
	"github.com/dapr/dapr/pkg/acl"
	"github.com/dapr/dapr/pkg/apphealth"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	env "github.com/dapr/dapr/pkg/config/env"
	config "github.com/dapr/dapr/pkg/config/modes"
>>>>>>> f302bf0d3 (Move all CLI flag options into consistent options package.)
	"github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	operatorV1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	resiliencyConfig "github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/utils"
)

const (
	// DefaultDaprHTTPPort is the default http port for Dapr.
	DefaultDaprHTTPPort = 3500
	// DefaultDaprPublicPort is the default http port for Dapr.
	DefaultDaprPublicPort = 3501
	// DefaultDaprAPIGRPCPort is the default API gRPC port for Dapr.
	DefaultDaprAPIGRPCPort = 50001
	// DefaultProfilePort is the default port for profiling endpoints.
	DefaultProfilePort = 7777
	// DefaultMetricsPort is the default port for metrics endpoints.
	DefaultMetricsPort = 9090
	// DefaultMaxRequestBodySize is the default option for the maximum body size in MB for Dapr HTTP servers.
	DefaultMaxRequestBodySize = 4
	// DefaultAPIListenAddress is which address to listen for the Dapr HTTP and GRPC APIs. Empty string is all addresses.
	DefaultAPIListenAddress = ""
	// DefaultReadBufferSize is the default option for the maximum header size in KB for Dapr HTTP servers.
	DefaultReadBufferSize = 4
	// DefaultGracefulShutdownDuration is the default option for the duration of the graceful shutdown.
	DefaultGracefulShutdownDuration = time.Second * 5
	// DefaultAppHealthCheckPath is the default path for HTTP health checks.
	DefaultAppHealthCheckPath = "/healthz"
	// DefaultChannelAddress is the default local network address that user application listen on.
	DefaultChannelAddress = "127.0.0.1"
)

// Config holds the Dapr Runtime configuration.
type Config struct {
	ID                           string
	HTTPPort                     int
	PublicPort                   *int
	ProfilePort                  int
	EnableProfiling              bool
	APIGRPCPort                  int
	InternalGRPCPort             int
	ApplicationPort              int
	APIListenAddresses           []string
	Mode                         modes.DaprMode
	PlacementAddresses           []string
	AllowedOrigins               string
	Standalone                   config.StandaloneConfig
	Kubernetes                   config.KubernetesConfig
	MaxConcurrency               int
	MTLSEnabled                  bool
	SentryServiceAddress         string
	MaxRequestBodySize           int
	UnixDomainSocket             string
	ReadBufferSize               int
	GracefulShutdownDuration     time.Duration
	EnableAPILogging             *bool
	DisableBuiltinK8sSecretStore bool
	AppHealthCheck               *apphealth.Config
	AppHealthCheckHTTPPath       string
	AppChannelAddress            string
	ConfigPath                   string
	Metrics                      *metrics.Options
	certChain                    *credentials.CertChain
}

// FromConfig creates a new Dapr Runtime from a configuration.
func FromConfig(cfg *Config) (*DaprRuntime, error) {
	// set environment variables
	// TODO - consider adding host address to runtime config and/or caching result in utils package
	host, err := utils.GetHostAddress()
	if err != nil {
		log.Warnf("failed to get host address, env variable %s will not be set", env.HostAddress)
	}

	variables := map[string]string{
		env.AppID:           cfg.ID,
		env.AppPort:         strconv.Itoa(cfg.ApplicationPort),
		env.HostAddress:     host,
		env.DaprPort:        strconv.Itoa(cfg.InternalGRPCPort),
		env.DaprGRPCPort:    strconv.Itoa(cfg.APIGRPCPort),
		env.DaprHTTPPort:    strconv.Itoa(cfg.HTTPPort),
		env.DaprMetricsPort: cfg.Metrics.Port,
		env.DaprProfilePort: strconv.Itoa(cfg.ProfilePort),
	}

	if err = utils.SetEnvVariables(variables); err != nil {
		return nil, err
	}

	var globalConfig *daprGlobalConfig.Configuration
	var configErr error

	if cfg.MTLSEnabled || cfg.Mode == modes.KubernetesMode {
		cfg.certChain, err = security.GetCertChain()
		if err != nil {
			return nil, err
		}
	}

	// Config and resiliency need the operator client
	var operatorClient operatorV1.OperatorClient
	if cfg.Mode == modes.KubernetesMode {
		log.Info("Initializing the operator client")
		client, conn, clientErr := client.GetOperatorClient(context.TODO(), cfg.Kubernetes.ControlPlaneAddress, security.TLSServerName, cfg.certChain)
		if clientErr != nil {
			return nil, clientErr
		}
		defer conn.Close()
		operatorClient = client
	}

	var accessControlList *daprGlobalConfig.AccessControlList
	namespace := os.Getenv("NAMESPACE")
	podName := os.Getenv("POD_NAME")

	if cfg.ConfigPath != "" {
		switch modes.DaprMode(cfg.Mode) {
		case modes.KubernetesMode:
			log.Debug("Loading Kubernetes config resource: " + cfg.ConfigPath)
			globalConfig, configErr = daprGlobalConfig.LoadKubernetesConfiguration(cfg.ConfigPath, namespace, podName, operatorClient)
		case modes.StandaloneMode:
			log.Debug("Loading config from file: " + cfg.ConfigPath)
			globalConfig, _, configErr = daprGlobalConfig.LoadStandaloneConfiguration(cfg.ConfigPath)
		}
	}

	if configErr != nil {
		log.Fatalf("error loading configuration: %s", configErr)
	}
	if globalConfig == nil {
		log.Info("loading default configuration")
		globalConfig = daprGlobalConfig.LoadDefaultConfiguration()
	}
	daprGlobalConfig.SetTracingSpecFromEnv(globalConfig)

	globalConfig.LoadFeatures()
	if enabledFeatures := globalConfig.EnabledFeatures(); len(enabledFeatures) > 0 {
		log.Info("Enabled features: " + strings.Join(enabledFeatures, " "))
	}

	// Initialize metrics only if MetricSpec is enabled.
	if globalConfig.Spec.MetricSpec.Enabled {
		if mErr := diag.InitMetrics(cfg.ID, namespace, globalConfig.Spec.MetricSpec.Rules); mErr != nil {
			log.Errorf(NewInitError(InitFailure, "metrics", mErr).Error())
		}
	}

	// Load Resiliency
	var resiliencyProvider *resiliencyConfig.Resiliency
	switch modes.DaprMode(cfg.Mode) {
	case modes.KubernetesMode:
		resiliencyConfigs := resiliencyConfig.LoadKubernetesResiliency(log, cfg.ID, namespace, operatorClient)
		log.Debugf("Found %d resiliency configurations from Kubernetes", len(resiliencyConfigs))
		resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
	case modes.StandaloneMode:
		if len(cfg.Standalone.ResourcesPath) > 0 {
			resiliencyConfigs := resiliencyConfig.LoadLocalResiliency(log, cfg.ID, cfg.Standalone.ResourcesPath...)
			log.Debugf("Found %d resiliency configurations in resources path", len(resiliencyConfigs))
			resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
		} else {
			resiliencyProvider = resiliencyConfig.FromConfigurations(log)
		}
	}
	log.Info("Resiliency configuration loaded")

	accessControlList, err = acl.ParseAccessControlSpec(
		globalConfig.Spec.AccessControlSpec,
		cfg.ApplicationProtocol.IsHTTP(),
	)
	if err != nil {
		log.Fatalf(err.Error())
	}

	// API logging can be enabled for this app or for every app, globally in the config
	if cfg.EnableAPILogging == nil {
		cfg.EnableAPILogging = &globalConfig.Spec.LoggingSpec.APILogging.Enabled
	}

	return newDaprRuntime(cfg, globalConfig, accessControlList, resiliencyProvider), nil
}
