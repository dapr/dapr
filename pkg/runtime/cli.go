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

//nolint:forbidigo
package runtime

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/acl"
	"github.com/dapr/dapr/pkg/buildinfo"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	env "github.com/dapr/dapr/pkg/config/env"
	"github.com/dapr/dapr/pkg/config/protocol"
	"github.com/dapr/dapr/pkg/cors"
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
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

// FromFlags parses command flags and returns DaprRuntime instance.
func FromFlags(args []string) (*DaprRuntime, error) {
	mode := flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	daprHTTPPort := flag.String("dapr-http-port", strconv.Itoa(DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	daprAPIListenAddresses := flag.String("dapr-listen-addresses", DefaultAPIListenAddress, "One or more addresses for the Dapr API to listen on, CSV limited")
	daprPublicPort := flag.String("dapr-public-port", "", "Public port for Dapr Health and Metadata to listen on")
	daprAPIGRPCPort := flag.String("dapr-grpc-port", strconv.Itoa(DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	daprInternalGRPCPort := flag.String("dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	appPort := flag.String("app-port", "", "The port the application is listening on")
	profilePort := flag.String("profile-port", strconv.Itoa(DefaultProfilePort), "The port for the profile server")
	appProtocolPtr := flag.String("app-protocol", string(protocol.HTTPProtocol), "Protocol for the application: grpc, grpcs, http, https, h2c")
	componentsPath := flag.String("components-path", "", "Alias for --resources-path [Deprecated, use --resources-path]")
	var resourcesPath stringSliceFlag
	flag.Var(&resourcesPath, "resources-path", "Path for resources directory. If not specified, no resources will be loaded. Can be passed multiple times")
	config := flag.String("config", "", "Path to config file, or name of a configuration object")
	appID := flag.String("app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	controlPlaneAddress := flag.String("control-plane-address", "", "Address for a Dapr control plane")
	sentryAddress := flag.String("sentry-address", "", "Address for the Sentry CA service")
	placementServiceHostAddr := flag.String("placement-host-address", "", "Addresses for Dapr Actor Placement servers")
	allowedOrigins := flag.String("allowed-origins", cors.DefaultAllowedOrigins, "Allowed HTTP origins")
	enableProfiling := flag.Bool("enable-profiling", false, "Enable profiling")
	runtimeVersion := flag.Bool("version", false, "Prints the runtime version")
	buildInfo := flag.Bool("build-info", false, "Prints the build info")
	waitCommand := flag.Bool("wait", false, "wait for Dapr outbound ready")
	appMaxConcurrency := flag.Int("app-max-concurrency", -1, "Controls the concurrency level when forwarding requests to user code; set to -1 for no limits")
	enableMTLS := flag.Bool("enable-mtls", false, "Enables automatic mTLS for daprd to daprd communication channels")
	appSSL := flag.Bool("app-ssl", false, "Sets the URI scheme of the app to https and attempts a TLS connection [Deprecated, use '--app-protocol https|grpcs']")
	daprHTTPMaxRequestSize := flag.Int("dapr-http-max-request-size", DefaultMaxRequestBodySize, "Increasing max size of request body in MB to handle uploading of big files")
	unixDomainSocket := flag.String("unix-domain-socket", "", "Path to a unix domain socket dir mount. If specified, Dapr API servers will use Unix Domain Sockets")
	daprHTTPReadBufferSize := flag.Int("dapr-http-read-buffer-size", DefaultReadBufferSize, "Increasing max size of read buffer in KB to handle sending multi-KB headers")
	daprGracefulShutdownSeconds := flag.Int("dapr-graceful-shutdown-seconds", int(DefaultGracefulShutdownDuration/time.Second), "Graceful shutdown time in seconds")
	enableAPILogging := flag.Bool("enable-api-logging", false, "Enable API logging for API calls")
	disableBuiltinK8sSecretStore := flag.Bool("disable-builtin-k8s-secret-store", false, "Disable the built-in Kubernetes Secret Store")
	enableAppHealthCheck := flag.Bool("enable-app-health-check", false, "Enable health checks for the application using the protocol defined with app-protocol")
	appHealthCheckPath := flag.String("app-health-check-path", DefaultAppHealthCheckPath, "Path used for health checks; HTTP only")
	appHealthProbeInterval := flag.Int("app-health-probe-interval", int(daprGlobalConfig.AppHealthConfigDefaultProbeInterval/time.Second), "Interval to probe for the health of the app in seconds")
	appHealthProbeTimeout := flag.Int("app-health-probe-timeout", int(daprGlobalConfig.AppHealthConfigDefaultProbeTimeout/time.Millisecond), "Timeout for app health probes in milliseconds")
	appHealthThreshold := flag.Int("app-health-threshold", int(daprGlobalConfig.AppHealthConfigDefaultThreshold), "Number of consecutive failures for the app to be considered unhealthy")

	appChannelAddress := flag.String("app-channel-address", DefaultChannelAddress, "The network address the application listens on")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)

	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	// Finally parse the CLI flags!
	// Ignore errors; CommandLine is set for ExitOnError.
	flag.CommandLine.Parse(args)

	// flag.Parse() will always set a value to "enableAPILogging", and it will be false whether it's explicitly set to false or unset
	// For this flag, we need the third state (unset) so we need to do a bit more work here to check if it's unset, then mark "enableAPILogging" as nil
	// It's not the prettiest approach, but…
	if !*enableAPILogging {
		enableAPILogging = nil
		for _, v := range args {
			if strings.HasPrefix(v, "--enable-api-logging") || strings.HasPrefix(v, "-enable-api-logging") {
				// This means that enable-api-logging was explicitly set to false
				enableAPILogging = ptr.Of(false)
				break
			}
		}
	}

	if len(resourcesPath) == 0 && *componentsPath != "" {
		resourcesPath = stringSliceFlag{*componentsPath}
	}

	if *runtimeVersion {
		fmt.Println(buildinfo.Version())
		os.Exit(0)
	}

	if *buildInfo {
		fmt.Printf("Version: %s\nGit Commit: %s\nGit Version: %s\n", buildinfo.Version(), buildinfo.Commit(), buildinfo.GitVersion())
		os.Exit(0)
	}

	if *waitCommand {
		waitUntilDaprOutboundReady(*daprHTTPPort)
		os.Exit(0)
	}

	if *mode == string(modes.StandaloneMode) {
		if err := validation.ValidateSelfHostedAppID(*appID); err != nil {
			return nil, err
		}
	}

	// Apply options to all loggers
	loggerOptions.SetAppID(*appID)
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		return nil, err
	}

	log.Infof("starting Dapr Runtime -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	daprHTTP, err := strconv.Atoi(*daprHTTPPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-http-port flag: %w", err)
	}

	daprAPIGRPC, err := strconv.Atoi(*daprAPIGRPCPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-grpc-port flag: %w", err)
	}

	profPort, err := strconv.Atoi(*profilePort)
	if err != nil {
		return nil, fmt.Errorf("error parsing profile-port flag: %w", err)
	}

	var daprInternalGRPC int
	if *daprInternalGRPCPort != "" && *daprInternalGRPCPort != "0" {
		daprInternalGRPC, err = strconv.Atoi(*daprInternalGRPCPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing dapr-internal-grpc-port: %w", err)
		}
	} else {
		// Get a "stable random" port in the range 47300-49,347 if it can be acquired using a deterministic algorithm that returns the same value if the same app is restarted
		// Otherwise, the port will be random.
		daprInternalGRPC, err = utils.GetStablePort(47300, *appID)
		if err != nil {
			return nil, fmt.Errorf("failed to get free port for internal grpc server: %w", err)
		}
	}

	var publicPort *int
	if *daprPublicPort != "" {
		port, cerr := strconv.Atoi(*daprPublicPort)
		if cerr != nil {
			return nil, fmt.Errorf("error parsing dapr-public-port: %w", cerr)
		}
		publicPort = &port
	}

	var applicationPort int
	if *appPort != "" {
		applicationPort, err = strconv.Atoi(*appPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing app-port: %w", err)
		}
	}

	if applicationPort == daprHTTP {
		return nil, fmt.Errorf("the 'dapr-http-port' argument value %d conflicts with 'app-port'", daprHTTP)
	}

	if applicationPort == daprAPIGRPC {
		return nil, fmt.Errorf("the 'dapr-grpc-port' argument value %d conflicts with 'app-port'", daprAPIGRPC)
	}

	var maxRequestBodySize int
	if *daprHTTPMaxRequestSize != -1 {
		maxRequestBodySize = *daprHTTPMaxRequestSize
	} else {
		maxRequestBodySize = DefaultMaxRequestBodySize
	}

	var readBufferSize int
	if *daprHTTPReadBufferSize != -1 {
		readBufferSize = *daprHTTPReadBufferSize
	} else {
		readBufferSize = DefaultReadBufferSize
	}

	var gracefulShutdownDuration time.Duration
	if *daprGracefulShutdownSeconds < 0 {
		gracefulShutdownDuration = DefaultGracefulShutdownDuration
	} else {
		gracefulShutdownDuration = time.Duration(*daprGracefulShutdownSeconds) * time.Second
	}

	placementAddresses := []string{}
	if *placementServiceHostAddr != "" {
		placementAddresses = parsePlacementAddr(*placementServiceHostAddr)
	}

	var concurrency int
	if *appMaxConcurrency != -1 {
		concurrency = *appMaxConcurrency
	}

	var appProtocol string
	{
		p := strings.ToLower(*appProtocolPtr)
		switch p {
		case string(protocol.GRPCSProtocol), string(protocol.HTTPSProtocol), string(protocol.H2CProtocol):
			appProtocol = p
		case string(protocol.HTTPProtocol):
			// For backwards compatibility, when protocol is HTTP and --app-ssl is set, use "https"
			// TODO: Remove in a future Dapr version
			if *appSSL {
				log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=https' instead")
				appProtocol = string(protocol.HTTPSProtocol)
			} else {
				appProtocol = string(protocol.HTTPProtocol)
			}
		case string(protocol.GRPCProtocol):
			// For backwards compatibility, when protocol is GRPC and --app-ssl is set, use "grpcs"
			// TODO: Remove in a future Dapr version
			if *appSSL {
				log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=grpcs' instead")
				appProtocol = string(protocol.GRPCSProtocol)
			} else {
				appProtocol = string(protocol.GRPCProtocol)
			}
		case "":
			appProtocol = string(protocol.HTTPProtocol)
		default:
			return nil, fmt.Errorf("invalid value for 'app-protocol': %v", *appProtocolPtr)
		}
	}

	daprAPIListenAddressList := strings.Split(*daprAPIListenAddresses, ",")
	if len(daprAPIListenAddressList) == 0 {
		daprAPIListenAddressList = []string{DefaultAPIListenAddress}
	}

	var healthProbeInterval time.Duration
	if *appHealthProbeInterval <= 0 {
		healthProbeInterval = daprGlobalConfig.AppHealthConfigDefaultProbeInterval
	} else {
		healthProbeInterval = time.Duration(*appHealthProbeInterval) * time.Second
	}

	var healthProbeTimeout time.Duration
	if *appHealthProbeTimeout <= 0 {
		healthProbeTimeout = daprGlobalConfig.AppHealthConfigDefaultProbeTimeout
	} else {
		healthProbeTimeout = time.Duration(*appHealthProbeTimeout) * time.Millisecond
	}

	if healthProbeTimeout > healthProbeInterval {
		return nil, errors.New("value for 'health-probe-timeout' must be smaller than 'health-probe-interval'")
	}

	// Also check to ensure no overflow with int32
	var healthThreshold int32
	if *appHealthThreshold < 1 || int32(*appHealthThreshold+1) < 0 {
		healthThreshold = daprGlobalConfig.AppHealthConfigDefaultThreshold
	} else {
		healthThreshold = int32(*appHealthThreshold)
	}

	runtimeConfig := NewRuntimeConfig(NewRuntimeConfigOpts{
		ID:                           *appID,
		PlacementAddresses:           placementAddresses,
		ControlPlaneAddress:          *controlPlaneAddress,
		AllowedOrigins:               *allowedOrigins,
		ResourcesPath:                resourcesPath,
		AppProtocol:                  appProtocol,
		Mode:                         *mode,
		HTTPPort:                     daprHTTP,
		InternalGRPCPort:             daprInternalGRPC,
		APIGRPCPort:                  daprAPIGRPC,
		APIListenAddresses:           daprAPIListenAddressList,
		PublicPort:                   publicPort,
		AppPort:                      applicationPort,
		ProfilePort:                  profPort,
		EnableProfiling:              *enableProfiling,
		MaxConcurrency:               concurrency,
		MTLSEnabled:                  *enableMTLS,
		SentryAddress:                *sentryAddress,
		MaxRequestBodySize:           maxRequestBodySize,
		UnixDomainSocket:             *unixDomainSocket,
		ReadBufferSize:               readBufferSize,
		GracefulShutdownDuration:     gracefulShutdownDuration,
		DisableBuiltinK8sSecretStore: *disableBuiltinK8sSecretStore,
		EnableAppHealthCheck:         *enableAppHealthCheck,
		AppHealthCheckPath:           *appHealthCheckPath,
		AppHealthProbeInterval:       healthProbeInterval,
		AppHealthProbeTimeout:        healthProbeTimeout,
		AppHealthThreshold:           healthThreshold,
		AppChannelAddress:            *appChannelAddress,
	})

	// set environment variables
	// TODO - consider adding host address to runtime config and/or caching result in utils package
	host, err := utils.GetHostAddress()
	if err != nil {
		log.Warnf("failed to get host address, env variable %s will not be set", env.HostAddress)
	}

	variables := map[string]string{
		env.AppID:           *appID,
		env.AppPort:         *appPort,
		env.HostAddress:     host,
		env.DaprPort:        strconv.Itoa(daprInternalGRPC),
		env.DaprGRPCPort:    *daprAPIGRPCPort,
		env.DaprHTTPPort:    *daprHTTPPort,
		env.DaprMetricsPort: metricsExporter.Options().Port, // TODO - consider adding to runtime config
		env.DaprProfilePort: *profilePort,
	}

	if err = utils.SetEnvVariables(variables); err != nil {
		return nil, err
	}

	var globalConfig *daprGlobalConfig.Configuration
	var configErr error

	if *enableMTLS || *mode == string(modes.KubernetesMode) {
		runtimeConfig.CertChain, err = security.GetCertChain()
		if err != nil {
			return nil, err
		}
	}

	// Config and resiliency need the operator client
	var operatorClient operatorV1.OperatorClient
	if *mode == string(modes.KubernetesMode) {
		log.Info("Initializing the operator client")
		client, conn, clientErr := client.GetOperatorClient(context.TODO(), *controlPlaneAddress, security.TLSServerName, runtimeConfig.CertChain)
		if clientErr != nil {
			return nil, clientErr
		}
		defer conn.Close()
		operatorClient = client
	}

	var accessControlList *daprGlobalConfig.AccessControlList
	namespace := os.Getenv("NAMESPACE")
	podName := os.Getenv("POD_NAME")

	if *config != "" {
		switch modes.DaprMode(*mode) {
		case modes.KubernetesMode:
			log.Debug("Loading Kubernetes config resource: " + *config)
			globalConfig, configErr = daprGlobalConfig.LoadKubernetesConfiguration(*config, namespace, podName, operatorClient)
		case modes.StandaloneMode:
			log.Debug("Loading config from file: " + *config)
			globalConfig, _, configErr = daprGlobalConfig.LoadStandaloneConfiguration(*config)
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
		if mErr := diag.InitMetrics(runtimeConfig.ID, namespace, globalConfig.Spec.MetricSpec.Rules); mErr != nil {
			log.Errorf(rterrors.NewInit(rterrors.InitFailure, "metrics", mErr).Error())
		}
	}

	// Load Resiliency
	var resiliencyProvider *resiliencyConfig.Resiliency
	switch modes.DaprMode(*mode) {
	case modes.KubernetesMode:
		resiliencyConfigs := resiliencyConfig.LoadKubernetesResiliency(log, *appID, namespace, operatorClient)
		log.Debugf("Found %d resiliency configurations from Kubernetes", len(resiliencyConfigs))
		resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
	case modes.StandaloneMode:
		if len(resourcesPath) > 0 {
			resiliencyConfigs := resiliencyConfig.LoadLocalResiliency(log, *appID, resourcesPath...)
			log.Debugf("Found %d resiliency configurations in resources path", len(resiliencyConfigs))
			resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
		} else {
			resiliencyProvider = resiliencyConfig.FromConfigurations(log)
		}
	}
	log.Info("Resiliency configuration loaded")

	accessControlList, err = acl.ParseAccessControlSpec(
		globalConfig.Spec.AccessControlSpec,
		runtimeConfig.AppConnectionConfig.Protocol.IsHTTP(),
	)
	if err != nil {
		log.Fatalf(err.Error())
	}

	// API logging can be enabled for this app or for every app, globally in the config
	if enableAPILogging != nil {
		runtimeConfig.EnableAPILogging = *enableAPILogging
	} else {
		runtimeConfig.EnableAPILogging = globalConfig.Spec.LoggingSpec.APILogging.Enabled
	}

	return NewDaprRuntime(runtimeConfig, globalConfig, accessControlList, resiliencyProvider), nil
}

func parsePlacementAddr(val string) []string {
	parsed := []string{}
	p := strings.Split(val, ",")
	for _, addr := range p {
		parsed = append(parsed, strings.TrimSpace(addr))
	}
	return parsed
}

// Flag type. Allows passing a flag multiple times to get a slice of strings.
// It implements the flag.Value interface.
type stringSliceFlag []string

// String formats the flag value.
func (f stringSliceFlag) String() string {
	return strings.Join(f, ",")
}

// Set the flag value.
func (f *stringSliceFlag) Set(value string) error {
	if value == "" {
		return errors.New("value is empty")
	}
	*f = append(*f, value)
	return nil
}
