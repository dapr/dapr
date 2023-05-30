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
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/config"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	env "github.com/dapr/dapr/pkg/config/env"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	operatorV1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	resiliencyConfig "github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/validation"
	"github.com/dapr/dapr/utils"
	"github.com/labstack/gommon/log"
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
	PlacementServiceHostAddr     string
	DaprAPIListenAddresses       string
	AppHealthProbeInterval       int
	AppHealthProbeTimeout        int
	AppHealthThreshold           int
	EnableAppHealthCheck         bool
	Mode                         string
	ConfigPath                   string
	UnixDomainSocket             string
	DaprHTTPReadBufferSize       int
	DisableBuiltinK8sSecretStore bool
	AppHealthCheckPath           string
	AppChannelAddress            string
	Metrics                      *metrics.Options
}

type internalConfig struct {
	id                           string
	httpPort                     int
	publicPort                   *int
	profilePort                  int
	enableProfiling              bool
	apiGRPCPort                  int
	internalGRPCPort             int
	applicationPort              int
	apiListenAddresses           []string
	applicationProtocol          Protocol
	mode                         modes.DaprMode
	placementAddresses           []string
	allowedOrigins               string
	standalone                   config.StandaloneConfig
	kubernetes                   config.KubernetesConfig
	maxConcurrency               int
	mTLSEnabled                  bool
	sentryServiceAddress         string
	maxRequestBodySize           int
	unixDomainSocket             string
	readBufferSize               int
	gracefulShutdownDuration     time.Duration
	enableAPILogging             *bool
	disableBuiltinK8sSecretStore bool
	appHealthCheck               *apphealth.Config
	appHealthCheckHTTPPath       string
	appChannelAddress            string
	configPath                   string
	certChain                    *credentials.CertChain
}

// FromConfig creates a new Dapr Runtime from a configuration.
func FromConfig(cfg *Config) (*DaprRuntime, error) {
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

	// Initialize dapr metrics exporter
	metricsExporter := metrics.NewExporterWithOptions(metrics.DefaultMetricNamespace, cfg.Metrics)
	if err = metricsExporter.Init(); err != nil {
		return nil, err
	}

	variables := map[string]string{
		env.AppID:           intc.id,
		env.AppPort:         strconv.Itoa(intc.applicationPort),
		env.HostAddress:     host,
		env.DaprPort:        strconv.Itoa(intc.internalGRPCPort),
		env.DaprGRPCPort:    strconv.Itoa(intc.apiGRPCPort),
		env.DaprHTTPPort:    strconv.Itoa(intc.httpPort),
		env.DaprMetricsPort: metricsExporter.Options().Port,
		env.DaprProfilePort: strconv.Itoa(intc.profilePort),
	}

	if err = utils.SetEnvVariables(variables); err != nil {
		return nil, err
	}

	var globalConfig *daprGlobalConfig.Configuration
	var configErr error

	if intc.mTLSEnabled || intc.mode == modes.KubernetesMode {
		intc.certChain, err = security.GetCertChain()
		if err != nil {
			return nil, err
		}
	}

	// Config and resiliency need the operator client
	var operatorClient operatorV1.OperatorClient
	if intc.mode == modes.KubernetesMode {
		log.Info("Initializing the operator client")
		client, conn, clientErr := client.GetOperatorClient(context.TODO(), intc.kubernetes.ControlPlaneAddress, security.TLSServerName, intc.certChain)
		if clientErr != nil {
			return nil, clientErr
		}
		defer conn.Close()
		operatorClient = client
	}

	var accessControlList *daprGlobalConfig.AccessControlList
	namespace := os.Getenv("NAMESPACE")
	podName := os.Getenv("POD_NAME")

	if intc.configPath != "" {
		switch intc.mode {
		case modes.KubernetesMode:
			log.Debug("Loading Kubernetes config resource: " + intc.configPath)
			globalConfig, configErr = daprGlobalConfig.LoadKubernetesConfiguration(intc.configPath, namespace, podName, operatorClient)
		case modes.StandaloneMode:
			log.Debug("Loading config from file: " + intc.configPath)
			globalConfig, _, configErr = daprGlobalConfig.LoadStandaloneConfiguration(intc.configPath)
		}
	}

	if configErr != nil {
		return nil, fmt.Errorf("error loading configuration: %s", configErr)
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
		if mErr := diag.InitMetrics(intc.id, namespace, globalConfig.Spec.MetricSpec.Rules); mErr != nil {
			log.Errorf(rterrors.NewInit(rterrors.InitFailure, "metrics", mErr).Error())
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

	accessControlList, err = acl.ParseAccessControlSpec(
		globalConfig.Spec.AccessControlSpec,
		intc.applicationProtocol.IsHTTP(),
	)
	if err != nil {
		return nil, err
	}

	// API logging can be enabled for this app or for every app, globally in the config
	if intc.enableAPILogging == nil {
		intc.enableAPILogging = &globalConfig.Spec.LoggingSpec.APILogging.Enabled
	}

	return newDaprRuntime(intc, globalConfig, accessControlList, resiliencyProvider), nil
}

func (c *Config) toInternal() (*internalConfig, error) {
	intc := &internalConfig{
		id:                   c.AppID,
		mode:                 modes.DaprMode(c.Mode),
		configPath:           c.ConfigPath,
		sentryServiceAddress: c.SentryAddress,
		allowedOrigins:       c.AllowedOrigins,
		kubernetes: config.KubernetesConfig{
			ControlPlaneAddress: c.ControlPlaneAddress,
		},
		standalone: config.StandaloneConfig{
			ResourcesPath: c.ResourcesPath,
		},
		enableProfiling:              c.EnableProfiling,
		mTLSEnabled:                  c.EnableMTLS,
		appChannelAddress:            c.AppChannelAddress,
		appHealthCheckHTTPPath:       c.AppHealthCheckPath,
		disableBuiltinK8sSecretStore: c.DisableBuiltinK8sSecretStore,
		unixDomainSocket:             c.UnixDomainSocket,
		maxConcurrency:               c.AppMaxConcurrency,
		maxRequestBodySize:           c.DaprHTTPMaxRequestSize,
		readBufferSize:               c.DaprHTTPReadBufferSize,
		enableAPILogging:             c.EnableAPILogging,
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
		intc.internalGRPCPort, err = utils.GetStablePort(47300, intc.id)
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

	if c.AppProtocol != "" {
		intc.applicationPort, err = strconv.Atoi(c.ApplicationPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing app-port: %w", err)
		}
	}

	if intc.applicationPort == intc.httpPort {
		return nil, fmt.Errorf("the 'dapr-http-port' argument value %d conflicts with 'app-port'", intc.httpPort)
	}

	if intc.applicationPort == intc.apiGRPCPort {
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

	if c.PlacementServiceHostAddr != "" {
		intc.placementAddresses = parsePlacementAddr(c.PlacementServiceHostAddr)
	}

	if intc.maxConcurrency == -1 {
		intc.maxConcurrency = 0
	}

	switch p := strings.ToLower(c.AppProtocol); p {
	case string(GRPCSProtocol):
		intc.applicationProtocol = GRPCSProtocol
	case string(HTTPSProtocol):
		intc.applicationProtocol = HTTPSProtocol
	case string(H2CProtocol):
		intc.applicationProtocol = H2CProtocol
	case string(HTTPProtocol):
		// For backwards compatibility, when protocol is HTTP and --app-ssl is set, use "https"
		// TODO: Remove in a future Dapr version
		if c.AppSSL {
			log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=https' instead")
			intc.applicationProtocol = HTTPSProtocol
		} else {
			intc.applicationProtocol = HTTPProtocol
		}
	case string(GRPCProtocol):
		// For backwards compatibility, when protocol is GRPC and --app-ssl is set, use "grpcs"
		// TODO: Remove in a future Dapr version
		if c.AppSSL {
			log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=grpcs' instead")
			intc.applicationProtocol = GRPCSProtocol
		} else {
			intc.applicationProtocol = GRPCProtocol
		}
	case "":
		intc.applicationProtocol = HTTPProtocol
	default:
		return nil, fmt.Errorf("invalid value for 'app-protocol': %v", c.AppProtocol)
	}

	intc.apiListenAddresses = strings.Split(c.DaprAPIListenAddresses, ",")
	if len(intc.apiListenAddresses) == 0 {
		intc.apiListenAddresses = []string{DefaultAPIListenAddress}
	}

	healthProbeInterval := time.Duration(c.AppHealthProbeInterval) * time.Second
	if c.AppHealthProbeInterval <= 0 {
		healthProbeInterval = apphealth.DefaultProbeInterval
	}

	healthProbeTimeout := time.Duration(c.AppHealthProbeTimeout) * time.Millisecond
	if c.AppHealthProbeTimeout <= 0 {
		healthProbeTimeout = apphealth.DefaultProbeTimeout
	}

	if healthProbeTimeout > healthProbeInterval {
		return nil, errors.New("value for 'health-probe-timeout' must be smaller than 'health-probe-interval'")
	}

	// Also check to ensure no overflow with int32
	healthThreshold := int32(c.AppHealthThreshold)
	if c.AppHealthThreshold < 1 || int32(c.AppHealthThreshold+1) < 0 {
		healthThreshold = apphealth.DefaultThreshold
	}

	if c.EnableAppHealthCheck {
		intc.appHealthCheck = &apphealth.Config{
			ProbeInterval: healthProbeInterval,
			ProbeTimeout:  healthProbeTimeout,
			ProbeOnly:     true,
			Threshold:     healthThreshold,
		}
	}

	return intc, nil
}

func parsePlacementAddr(val string) []string {
	parsed := []string{}
	p := strings.Split(val, ",")
	for _, addr := range p {
		parsed = append(parsed, strings.TrimSpace(addr))
	}
	return parsed
}
