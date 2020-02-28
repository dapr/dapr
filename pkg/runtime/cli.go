// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	global_config "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/version"
)

func FromFlags() (*DaprRuntime, error) {
	mode := flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	daprHTTPPort := flag.String("dapr-http-port", fmt.Sprintf("%v", DefaultDaprHTTPPort), "HTTP port for Dapr to listen on")
	daprGRPCPort := flag.String("dapr-grpc-port", fmt.Sprintf("%v", DefaultDaprGRPCPort), "gRPC port for Dapr to listen on")
	appPort := flag.String("app-port", "", "The port the application is listening on")
	profilePort := flag.String("profile-port", fmt.Sprintf("%v", DefaultProfilePort), "The port for the profile server")
	appProtocol := flag.String("protocol", string(HTTPProtocol), "Protocol for the application: gRPC or http")
	componentsPath := flag.String("components-path", DefaultComponentsPath, "Path for components directory. Standalone mode only")
	config := flag.String("config", "", "Path to config file, or name of a configuration object")
	appID := flag.String("app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	controlPlaneAddress := flag.String("control-plane-address", "", "Address for an Dapr control plane")
	sentryAddress := flag.String("sentry-address", "", "Address for the Sentry CA service")
	placementServiceAddress := flag.String("placement-address", "", "Address for the Dapr placement service")
	allowedOrigins := flag.String("allowed-origins", DefaultAllowedOrigins, "Allowed HTTP origins")
	enableProfiling := flag.String("enable-profiling", "false", fmt.Sprintf("Enable profiling. default is false"))
	runtimeVersion := flag.Bool("version", false, "prints the runtime version")
	maxConcurrency := flag.Int("max-concurrency", -1, "controls the concurrency level when forwarding requests to user code")
	mtlsEnabled := flag.Bool("enable-mtls", false, "Enables automatic mTLS for daprd to daprd communication channels")
	metricsPort := flag.String("metrics-port", fmt.Sprintf("%v", DefaultMetricsPort), "The port for the metrics server")
	enableMetrics := flag.String("enable-metrics", "true", fmt.Sprintf("Enable metrics. default is true"))

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	if *runtimeVersion {
		fmt.Println(version.Version())
		os.Exit(0)
	}

	// Apply options to all loggers
	loggerOptions.SetAppID(*appID)
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		return nil, err
	}

	log.Infof("starting Dapr Runtime -- version %s -- commit %s", version.Version(), version.Commit())
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	daprHTTP, err := strconv.Atoi(*daprHTTPPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-http-port flag: %s", err)
	}

	daprGRPC, err := strconv.Atoi(*daprGRPCPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-grpc-port flag: %s", err)
	}

	profPort, err := strconv.Atoi(*profilePort)
	if err != nil {
		return nil, fmt.Errorf("error parsing profile-port flag: %s", err)
	}

	metrPort, err := strconv.Atoi(*metricsPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing metrics-port flag: %s", err)
	}

	applicationPort := 0
	if *appPort != "" {
		applicationPort, err = strconv.Atoi(*appPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing app-port: %s", err)
		}
	}

	enableProf, err := strconv.ParseBool(*enableProfiling)
	if err != nil {
		return nil, err
	}

	enableMetr, err := strconv.ParseBool(*enableMetrics)
	if err != nil {
		return nil, err
	}

	runtimeConfig := NewRuntimeConfig(*appID, *placementServiceAddress, *controlPlaneAddress, *allowedOrigins, *config, *componentsPath,
		*appProtocol, *mode, daprHTTP, daprGRPC, applicationPort, profPort, enableProf, *maxConcurrency, *mtlsEnabled, *sentryAddress, metrPort, enableMetr)

	var globalConfig *global_config.Configuration

	if *config != "" {
		switch modes.DaprMode(*mode) {
		case modes.KubernetesMode:
			globalConfig, err = global_config.LoadKubernetesConfiguration(*config, *controlPlaneAddress)
		case modes.StandaloneMode:
			globalConfig, err = global_config.LoadStandaloneConfiguration(*config)
		}
	} else {
		globalConfig = global_config.LoadDefaultConfiguration()
	}
	if err != nil {
		log.Warnf("error loading config: %s. loading default config", err)
	}

	return NewDaprRuntime(runtimeConfig, globalConfig), nil
}
