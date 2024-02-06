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
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/acl"
	"github.com/dapr/dapr/pkg/config"
	env "github.com/dapr/dapr/pkg/config/env"
	configmodes "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/config/protocol"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/ports"
	operatorV1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	resiliencyConfig "github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/validation"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/ptr"
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
	AppID                        string
	ControlPlaneAddress          string
	SentryAddress                string
	AllowedOrigins               string
	EnableProfiling              bool
	AppMaxConcurrency            int
	EnableMTLS                   bool
	AppSSL                       bool
	DaprHTTPMaxRequestSize       int
	ResourcesPath                []string
	ComponentsPath               string
	AppProtocol                  string
	EnableAPILogging             *bool
	DaprHTTPPort                 string
	DaprAPIGRPCPort              string
	ProfilePort                  string
	DaprInternalGRPCPort         string
	DaprPublicPort               string
	ApplicationPort              string
	DaprGracefulShutdownSeconds  int
	DaprBlockShutdownDuration    *time.Duration
	ActorsService                string
	RemindersService             string
	DaprAPIListenAddresses       string
	AppHealthProbeInterval       int
	AppHealthProbeTimeout        int
	AppHealthThreshold           int
	EnableAppHealthCheck         bool
	Mode                         string
	Config                       []string
	UnixDomainSocket             string
	DaprHTTPReadBufferSize       int
	DisableBuiltinK8sSecretStore bool
	AppHealthCheckPath           string
	AppChannelAddress            string
	Metrics                      *metrics.Options
	Registry                     *registry.Options
	Security                     security.Handler
}

type internalConfig struct {
	id                           string
	httpPort                     int
	publicPort                   *int
	profilePort                  int
	enableProfiling              bool
	apiGRPCPort                  int
	internalGRPCPort             int
	apiListenAddresses           []string
	appConnectionConfig          config.AppConnectionConfig
	mode                         modes.DaprMode
	actorsService                string
	remindersService             string
	allowedOrigins               string
	standalone                   configmodes.StandaloneConfig
	kubernetes                   configmodes.KubernetesConfig
	mTLSEnabled                  bool
	sentryServiceAddress         string
	maxRequestBodySize           int
	unixDomainSocket             string
	readBufferSize               int
	gracefulShutdownDuration     time.Duration
	blockShutdownDuration        *time.Duration
	enableAPILogging             *bool
	disableBuiltinK8sSecretStore bool
	config                       []string
	registry                     *registry.Registry
	metricsExporter              metrics.Exporter
}

func (i internalConfig) ActorsEnabled() bool {
	return i.actorsService != ""
}

// FromConfig creates a new Dapr Runtime from a configuration.
func FromConfig(ctx context.Context, cfg *Config) (*DaprRuntime, error) {
	intc, err := cfg.toInternal()
	if err != nil {
		return nil, err
	}

	// set environment variables
	// TODO - consider adding host address to runtime config and/or caching result in utils package
	host, err := utils.GetHostAddress()
	if err != nil {
		log.Warnf("failed to get host address, env variable %s will not be set", env.HostAddress)
	}

	variables := map[string]string{
		env.AppID:           intc.id,
		env.AppPort:         strconv.Itoa(intc.appConnectionConfig.Port),
		env.HostAddress:     host,
		env.DaprPort:        strconv.Itoa(intc.internalGRPCPort),
		env.DaprGRPCPort:    strconv.Itoa(intc.apiGRPCPort),
		env.DaprHTTPPort:    strconv.Itoa(intc.httpPort),
		env.DaprMetricsPort: intc.metricsExporter.Options().Port,
		env.DaprProfilePort: strconv.Itoa(intc.profilePort),
	}

	if err = utils.SetEnvVariables(variables); err != nil {
		return nil, err
	}

	// Config and resiliency need the operator client
	var operatorClient operatorV1.OperatorClient
	if intc.mode == modes.KubernetesMode {
		log.Info("Initializing the operator client")
		client, conn, clientErr := client.GetOperatorClient(ctx, cfg.ControlPlaneAddress, cfg.Security)
		if clientErr != nil {
			return nil, clientErr
		}
		defer conn.Close()
		operatorClient = client
	}

	namespace := os.Getenv("NAMESPACE")
	podName := os.Getenv("POD_NAME")

	var (
		globalConfig *config.Configuration
		configErr    error
	)

	if len(intc.config) > 0 {
		switch intc.mode {
		case modes.KubernetesMode:
			if len(intc.config) > 1 {
				// We are not returning an error here because in Kubernetes mode, the injector itself doesn't allow multiple configuration flags to be added, so this should never happen in normal environments
				log.Warnf("Multiple configurations are not supported in Kubernetes mode; only the first one will be loaded")
			}
			log.Debug("Loading Kubernetes config resource: " + intc.config[0])
			globalConfig, configErr = config.LoadKubernetesConfiguration(intc.config[0], namespace, podName, operatorClient)
		case modes.StandaloneMode:
			log.Debug("Loading config from file(s): " + strings.Join(intc.config, ", "))
			globalConfig, configErr = config.LoadStandaloneConfiguration(intc.config...)
		}
	}

	if configErr != nil {
		return nil, fmt.Errorf("error loading configuration: %s", configErr)
	}
	if globalConfig == nil {
		log.Info("loading default configuration")
		globalConfig = config.LoadDefaultConfiguration()
	}
	config.SetTracingSpecFromEnv(globalConfig)

	globalConfig.LoadFeatures()
	if enabledFeatures := globalConfig.EnabledFeatures(); len(enabledFeatures) > 0 {
		log.Info("Enabled features: " + strings.Join(enabledFeatures, " "))
	}

	// Initialize metrics only if MetricSpec is enabled.
	metricsSpec := globalConfig.GetMetricsSpec()
	if metricsSpec.GetEnabled() {
		err = diag.InitMetrics(intc.id, namespace, metricsSpec.Rules, metricsSpec.GetHTTPIncreasedCardinality(log))
		if err != nil {
			log.Errorf(rterrors.NewInit(rterrors.InitFailure, "metrics", err).Error())
		}
	}

	// Load Resiliency
	var resiliencyProvider *resiliencyConfig.Resiliency
	switch intc.mode {
	case modes.KubernetesMode:
		resiliencyConfigs := resiliencyConfig.LoadKubernetesResiliency(log, intc.id, namespace, operatorClient)
		log.Debugf("Found %d resiliency configurations from Kubernetes", len(resiliencyConfigs))
		resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
	case modes.StandaloneMode:
		if len(intc.standalone.ResourcesPath) > 0 {
			resiliencyConfigs := resiliencyConfig.LoadLocalResiliency(log, intc.id, intc.standalone.ResourcesPath...)
			log.Debugf("Found %d resiliency configurations in resources path", len(resiliencyConfigs))
			resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
		} else {
			resiliencyProvider = resiliencyConfig.FromConfigurations(log)
		}
	}

	accessControlList, err := acl.ParseAccessControlSpec(
		globalConfig.Spec.AccessControlSpec,
		intc.appConnectionConfig.Protocol.IsHTTP(),
	)
	if err != nil {
		return nil, err
	}

	// API logging can be enabled for this app or for every app, globally in the config
	if intc.enableAPILogging == nil {
		intc.enableAPILogging = ptr.Of(globalConfig.GetAPILoggingSpec().Enabled)
	}

	return newDaprRuntime(ctx, cfg.Security, intc, globalConfig, accessControlList, resiliencyProvider)
}

func (c *Config) toInternal() (*internalConfig, error) {
	intc := &internalConfig{
		id:                   c.AppID,
		mode:                 modes.DaprMode(c.Mode),
		config:               c.Config,
		sentryServiceAddress: c.SentryAddress,
		allowedOrigins:       c.AllowedOrigins,
		kubernetes: configmodes.KubernetesConfig{
			ControlPlaneAddress: c.ControlPlaneAddress,
		},
		standalone: configmodes.StandaloneConfig{
			ResourcesPath: c.ResourcesPath,
		},
		enableProfiling:              c.EnableProfiling,
		mTLSEnabled:                  c.EnableMTLS,
		disableBuiltinK8sSecretStore: c.DisableBuiltinK8sSecretStore,
		unixDomainSocket:             c.UnixDomainSocket,
		maxRequestBodySize:           c.DaprHTTPMaxRequestSize,
		readBufferSize:               c.DaprHTTPReadBufferSize,
		enableAPILogging:             c.EnableAPILogging,
		appConnectionConfig: config.AppConnectionConfig{
			ChannelAddress:      c.AppChannelAddress,
			HealthCheckHTTPPath: c.AppHealthCheckPath,
			MaxConcurrency:      c.AppMaxConcurrency,
		},
		registry:              registry.New(c.Registry),
		metricsExporter:       metrics.NewExporterWithOptions(log, metrics.DefaultMetricNamespace, c.Metrics),
		blockShutdownDuration: c.DaprBlockShutdownDuration,
		actorsService:         c.ActorsService,
		remindersService:      c.RemindersService,
	}

	if len(intc.standalone.ResourcesPath) == 0 && c.ComponentsPath != "" {
		intc.standalone.ResourcesPath = []string{c.ComponentsPath}
	}

	if intc.mode == modes.StandaloneMode {
		if err := validation.ValidateSelfHostedAppID(intc.id); err != nil {
			return nil, err
		}
	}

	var err error
	intc.httpPort, err = strconv.Atoi(c.DaprHTTPPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-http-port flag: %w", err)
	}

	intc.apiGRPCPort, err = strconv.Atoi(c.DaprAPIGRPCPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-grpc-port flag: %w", err)
	}

	intc.profilePort, err = strconv.Atoi(c.ProfilePort)
	if err != nil {
		return nil, fmt.Errorf("error parsing profile-port flag: %w", err)
	}

	if c.DaprInternalGRPCPort != "" && c.DaprInternalGRPCPort != "0" {
		intc.internalGRPCPort, err = strconv.Atoi(c.DaprInternalGRPCPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing dapr-internal-grpc-port: %w", err)
		}
	} else {
		// Get a "stable random" port in the range 47300-49,347 if it can be
		// acquired using a deterministic algorithm that returns the same value if
		// the same app is restarted
		// Otherwise, the port will be random.
		intc.internalGRPCPort, err = ports.GetStablePort(47300, intc.id)
		if err != nil {
			return nil, fmt.Errorf("failed to get free port for internal grpc server: %w", err)
		}
	}

	if c.DaprPublicPort != "" {
		var port int
		port, err = strconv.Atoi(c.DaprPublicPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing dapr-public-port: %w", err)
		}
		intc.publicPort = &port
	}

	if c.ApplicationPort != "" {
		intc.appConnectionConfig.Port, err = strconv.Atoi(c.ApplicationPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing app-port: %w", err)
		}
	}

	if intc.appConnectionConfig.Port == intc.httpPort {
		return nil, fmt.Errorf("the 'dapr-http-port' argument value %d conflicts with 'app-port'", intc.httpPort)
	}

	if intc.appConnectionConfig.Port == intc.apiGRPCPort {
		return nil, fmt.Errorf("the 'dapr-grpc-port' argument value %d conflicts with 'app-port'", intc.apiGRPCPort)
	}

	if intc.maxRequestBodySize == -1 {
		intc.maxRequestBodySize = DefaultMaxRequestBodySize
	}

	if intc.readBufferSize == -1 {
		intc.readBufferSize = DefaultReadBufferSize
	}

	if c.DaprGracefulShutdownSeconds < 0 {
		intc.gracefulShutdownDuration = DefaultGracefulShutdownDuration
	} else {
		intc.gracefulShutdownDuration = time.Duration(c.DaprGracefulShutdownSeconds) * time.Second
	}

	if intc.appConnectionConfig.MaxConcurrency == -1 {
		intc.appConnectionConfig.MaxConcurrency = 0
	}

	switch p := strings.ToLower(c.AppProtocol); p {
	case string(protocol.GRPCSProtocol):
		intc.appConnectionConfig.Protocol = protocol.GRPCSProtocol
	case string(protocol.HTTPSProtocol):
		intc.appConnectionConfig.Protocol = protocol.HTTPSProtocol
	case string(protocol.H2CProtocol):
		intc.appConnectionConfig.Protocol = protocol.H2CProtocol
	case string(protocol.HTTPProtocol):
		// For backwards compatibility, when protocol is HTTP and --app-ssl is set, use "https"
		// TODO: Remove in a future Dapr version
		if c.AppSSL {
			log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=https' instead")
			intc.appConnectionConfig.Protocol = protocol.HTTPSProtocol
		} else {
			intc.appConnectionConfig.Protocol = protocol.HTTPProtocol
		}
	case string(protocol.GRPCProtocol):
		// For backwards compatibility, when protocol is GRPC and --app-ssl is set, use "grpcs"
		// TODO: Remove in a future Dapr version
		if c.AppSSL {
			log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=grpcs' instead")
			intc.appConnectionConfig.Protocol = protocol.GRPCSProtocol
		} else {
			intc.appConnectionConfig.Protocol = protocol.GRPCProtocol
		}
	case "":
		intc.appConnectionConfig.Protocol = protocol.HTTPProtocol
	default:
		return nil, fmt.Errorf("invalid value for 'app-protocol': %v", c.AppProtocol)
	}

	intc.apiListenAddresses = strings.Split(c.DaprAPIListenAddresses, ",")
	if len(intc.apiListenAddresses) == 0 {
		intc.apiListenAddresses = []string{DefaultAPIListenAddress}
	}

	healthProbeInterval := time.Duration(c.AppHealthProbeInterval) * time.Second
	if c.AppHealthProbeInterval <= 0 {
		healthProbeInterval = config.AppHealthConfigDefaultProbeInterval
	}

	healthProbeTimeout := time.Duration(c.AppHealthProbeTimeout) * time.Millisecond
	if c.AppHealthProbeTimeout <= 0 {
		healthProbeTimeout = config.AppHealthConfigDefaultProbeTimeout
	}

	if healthProbeTimeout > healthProbeInterval {
		return nil, errors.New("value for 'health-probe-timeout' must be smaller than 'health-probe-interval'")
	}

	// Also check to ensure no overflow with int32
	healthThreshold := int32(c.AppHealthThreshold)
	if c.AppHealthThreshold < 1 || int32(c.AppHealthThreshold+1) < 0 {
		healthThreshold = config.AppHealthConfigDefaultThreshold
	}

	if c.EnableAppHealthCheck {
		intc.appConnectionConfig.HealthCheck = &config.AppHealthConfig{
			ProbeInterval: healthProbeInterval,
			ProbeTimeout:  healthProbeTimeout,
			ProbeOnly:     true,
			Threshold:     healthThreshold,
		}
	}

	return intc, nil
}
