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
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/phayes/freeport"
	"github.com/pkg/errors"

	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"

	"github.com/dapr/dapr/pkg/acl"
	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	daprGlobalConfig "github.com/dapr/dapr/pkg/config"
	env "github.com/dapr/dapr/pkg/config/env"
	"github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	operatorV1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	resiliencyConfig "github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/validation"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
)

// FromFlags parses command flags and returns DaprRuntime instance.
func FromFlags() (*DaprRuntime, error) {
	mode := flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	daprHTTPPort := flag.String("dapr-http-port", strconv.Itoa(DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	daprAPIListenAddresses := flag.String("dapr-listen-addresses", DefaultAPIListenAddress, "One or more addresses for the Dapr API to listen on, CSV limited")
	daprPublicPort := flag.String("dapr-public-port", "", "Public port for Dapr Health and Metadata to listen on")
	daprAPIGRPCPort := flag.String("dapr-grpc-port", strconv.Itoa(DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	daprInternalGRPCPort := flag.String("dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	appPort := flag.String("app-port", "", "The port the application is listening on")
	profilePort := flag.String("profile-port", strconv.Itoa(DefaultProfilePort), "The port for the profile server")
	appProtocol := flag.String("app-protocol", string(HTTPProtocol), "Protocol for the application: grpc or http")
	componentsPath := flag.String("components-path", "", "Path for components directory. If empty, components will not be loaded. Self-hosted mode only")
	resourcesPath := flag.String("resources-path", "", "Path for resources directory. If empty, resources will not be loaded. Self-hosted mode only")
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
	appSSL := flag.Bool("app-ssl", false, "Sets the URI scheme of the app to https and attempts an SSL connection")
	daprHTTPMaxRequestSize := flag.Int("dapr-http-max-request-size", DefaultMaxRequestBodySize, "Increasing max size of request body in MB to handle uploading of big files")
	unixDomainSocket := flag.String("unix-domain-socket", "", "Path to a unix domain socket dir mount. If specified, Dapr API servers will use Unix Domain Sockets")
	daprHTTPReadBufferSize := flag.Int("dapr-http-read-buffer-size", DefaultReadBufferSize, "Increasing max size of read buffer in KB to handle sending multi-KB headers")
	daprGracefulShutdownSeconds := flag.Int("dapr-graceful-shutdown-seconds", int(DefaultGracefulShutdownDuration/time.Second), "Graceful shutdown time in seconds")
	enableAPILogging := flag.Bool("enable-api-logging", false, "Enable API logging for API calls")
	disableBuiltinK8sSecretStore := flag.Bool("disable-builtin-k8s-secret-store", false, "Disable the built-in Kubernetes Secret Store")
	enableAppHealthCheck := flag.Bool("enable-app-health-check", false, "Enable health checks for the application using the protocol defined with app-protocol")
	appHealthCheckPath := flag.String("app-health-check-path", DefaultAppHealthCheckPath, "Path used for health checks; HTTP only")
	appHealthProbeInterval := flag.Int("app-health-probe-interval", int(apphealth.DefaultProbeInterval/time.Second), "Interval to probe for the health of the app in seconds")
	appHealthProbeTimeout := flag.Int("app-health-probe-timeout", int(apphealth.DefaultProbeTimeout/time.Millisecond), "Timeout for app health probes in milliseconds")
	appHealthThreshold := flag.Int("app-health-threshold", int(apphealth.DefaultThreshold), "Number of consecutive failures for the app to be considered unhealthy")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)

	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	// flag.Parse() will always set a value to "enableAPILogging", and it will be false whether it's explicitly set to false or unset
	// For this flag, we need the third state (unset) so we need to do a bit more work here to check if it's unset, then mark "enableAPILogging" as nil
	// It's not the prettiest approach, but…
	if !*enableAPILogging {
		enableAPILogging = nil
		for _, v := range os.Args {
			if strings.HasPrefix(v, "--enable-api-logging") || strings.HasPrefix(v, "-enable-api-logging") {
				// This means that enable-api-logging was explicitly set to false
				enableAPILogging = ptr.Of(false)
				break
			}
		}
	}

	if *resourcesPath != "" {
		componentsPath = resourcesPath
	}

	if *runtimeVersion {
		fmt.Println(version.Version())
		os.Exit(0)
	}

	if *buildInfo {
		fmt.Printf("Version: %s\nGit Commit: %s\nGit Version: %s\n", version.Version(), version.Commit(), version.GitVersion())
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

	log.Infof("starting Dapr Runtime -- version %s -- commit %s", version.Version(), version.Commit())
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	daprHTTP, err := strconv.Atoi(*daprHTTPPort)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing dapr-http-port flag")
	}

	daprAPIGRPC, err := strconv.Atoi(*daprAPIGRPCPort)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing dapr-grpc-port flag")
	}

	profPort, err := strconv.Atoi(*profilePort)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing profile-port flag")
	}

	var daprInternalGRPC int
	if *daprInternalGRPCPort != "" && *daprInternalGRPCPort != "0" {
		daprInternalGRPC, err = strconv.Atoi(*daprInternalGRPCPort)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing dapr-internal-grpc-port")
		}
	} else {
		daprInternalGRPC, err = freeport.GetFreePort()
		if err != nil {
			return nil, errors.Wrap(err, "failed to get free port for internal grpc server")
		}
	}

	var publicPort *int
	if *daprPublicPort != "" {
		port, cerr := strconv.Atoi(*daprPublicPort)
		if cerr != nil {
			return nil, errors.Wrap(cerr, "error parsing dapr-public-port")
		}
		publicPort = &port
	}

	var applicationPort int
	if *appPort != "" {
		applicationPort, err = strconv.Atoi(*appPort)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing app-port")
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

	appPrtcl := string(HTTPProtocol)
	if *appProtocol != string(HTTPProtocol) {
		appPrtcl = *appProtocol
	}

	daprAPIListenAddressList := strings.Split(*daprAPIListenAddresses, ",")
	if len(daprAPIListenAddressList) == 0 {
		daprAPIListenAddressList = []string{DefaultAPIListenAddress}
	}

	var healthProbeInterval time.Duration
	if *appHealthProbeInterval <= 0 {
		healthProbeInterval = apphealth.DefaultProbeInterval
	} else {
		healthProbeInterval = time.Duration(*appHealthProbeInterval) * time.Second
	}

	var healthProbeTimeout time.Duration
	if *appHealthProbeTimeout <= 0 {
		healthProbeTimeout = apphealth.DefaultProbeTimeout
	} else {
		healthProbeTimeout = time.Duration(*appHealthProbeTimeout) * time.Millisecond
	}

	if healthProbeTimeout > healthProbeInterval {
		return nil, errors.New("value for 'health-probe-timeout' must be smaller than 'health-probe-interval'")
	}

	// Also check to ensure no overflow with int32
	var healthThreshold int32
	if *appHealthThreshold < 1 || int32(*appHealthThreshold+1) < 0 {
		healthThreshold = apphealth.DefaultThreshold
	} else {
		healthThreshold = int32(*appHealthThreshold)
	}

	runtimeConfig := NewRuntimeConfig(NewRuntimeConfigOpts{
		ID:                           *appID,
		PlacementAddresses:           placementAddresses,
		controlPlaneAddress:          *controlPlaneAddress,
		AllowedOrigins:               *allowedOrigins,
		GlobalConfig:                 *config,
		ComponentsPath:               *componentsPath,
		AppProtocol:                  appPrtcl,
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
		AppSSL:                       *appSSL,
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
		log.Infof("Initializing the operator client (config: %s)", *config)
		client, conn, clientErr := client.GetOperatorClient(*controlPlaneAddress, security.TLSServerName, runtimeConfig.CertChain)
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
			globalConfig, configErr = daprGlobalConfig.LoadKubernetesConfiguration(*config, namespace, podName, operatorClient)
		case modes.StandaloneMode:
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

	// TODO: Remove once AppHealthCheck feature is finalized
	if !daprGlobalConfig.IsFeatureEnabled(globalConfig.Spec.Features, daprGlobalConfig.AppHealthCheck) && *enableAppHealthCheck {
		log.Warnf("App health checks are a preview feature and require the %s feature flag to be enabled. See https://docs.dapr.io/operations/configuration/preview-features/ on how to enable preview features.", daprGlobalConfig.AppHealthCheck)
		runtimeConfig.AppHealthCheck = nil
	}

	resiliencyEnabled := daprGlobalConfig.IsFeatureEnabled(globalConfig.Spec.Features, daprGlobalConfig.Resiliency)
	var resiliencyProvider resiliencyConfig.Provider

	if resiliencyEnabled {
		var resiliencyConfigs []*resiliencyV1alpha.Resiliency
		switch modes.DaprMode(*mode) {
		case modes.KubernetesMode:
			resiliencyConfigs = resiliencyConfig.LoadKubernetesResiliency(log, *appID, namespace, operatorClient)
		case modes.StandaloneMode:
			resiliencyConfigs = resiliencyConfig.LoadStandaloneResiliency(log, *appID, *componentsPath)
		}
		log.Debugf("Found %d resiliency configurations.", len(resiliencyConfigs))
		resiliencyProvider = resiliencyConfig.FromConfigurations(log, resiliencyConfigs...)
		log.Info("Resiliency configuration loaded.")
	} else {
		log.Debug("Resiliency is not enabled.")
		resiliencyProvider = &resiliencyConfig.NoOp{}
	}

	accessControlList, err = acl.ParseAccessControlSpec(globalConfig.Spec.AccessControlSpec, string(runtimeConfig.ApplicationProtocol))
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
