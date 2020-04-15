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
	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/version"
)

// FromFlags parses command flags and returns DaprRuntime instance
func FromFlags() (*DaprRuntime, error) {
	mode := flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	daprHTTPPort := flag.String("dapr-http-port", fmt.Sprintf("%v", DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	daprAPIGRPCPort := flag.String("dapr-grpc-port", fmt.Sprintf("%v", DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	daprInternalGRPCPort := flag.String("dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	appPort := flag.String("app-port", "", "The port the application is listening on")
	profilePort := flag.String("profile-port", fmt.Sprintf("%v", DefaultProfilePort), "The port for the profile server")
	appProtocol := flag.String("protocol", string(HTTPProtocol), "Protocol for the application: grpc or http")
	componentsPath := flag.String("components-path", DefaultComponentsPath, "Path for components directory. Standalone mode only")
	config := flag.String("config", "", "Path to config file, or name of a configuration object")
	appID := flag.String("app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	controlPlaneAddress := flag.String("control-plane-address", "", "Address for a Dapr control plane")
	sentryAddress := flag.String("sentry-address", "", "Address for the Sentry CA service")
	placementServiceAddress := flag.String("placement-address", "", "Address for the Dapr placement service")
	allowedOrigins := flag.String("allowed-origins", DefaultAllowedOrigins, "Allowed HTTP origins")
	enableProfiling := flag.Bool("enable-profiling", false, "Enable profiling")
	runtimeVersion := flag.Bool("version", false, "Prints the runtime version")
	maxConcurrency := flag.Int("max-concurrency", -1, "Controls the concurrency level when forwarding requests to user code")
	enableMTLS := flag.Bool("enable-mtls", false, "Enables automatic mTLS for daprd to daprd communication channels")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

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

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	if err := diagnostics.InitMetrics(*appID); err != nil {
		log.Fatal(err)
	}

	daprHTTP, err := strconv.Atoi(*daprHTTPPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-http-port flag: %s", err)
	}

	daprAPIGRPC, err := strconv.Atoi(*daprAPIGRPCPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-grpc-port flag: %s", err)
	}

	profPort, err := strconv.Atoi(*profilePort)
	if err != nil {
		return nil, fmt.Errorf("error parsing profile-port flag: %s", err)
	}

	var daprInternalGRPC int
	if *daprInternalGRPCPort != "" {
		daprInternalGRPC, err = strconv.Atoi(*daprInternalGRPCPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing dapr-internal-grpc-port: %s", err)
		}
	} else {
		daprInternalGRPC, err = grpc.GetFreePort()
		if err != nil {
			return nil, fmt.Errorf("failed to get free port for internal grpc server: %s", err)
		}
	}

	var applicationPort int
	if *appPort != "" {
		applicationPort, err = strconv.Atoi(*appPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing app-port: %s", err)
		}
	}

	runtimeConfig := NewRuntimeConfig(*appID, *placementServiceAddress, *controlPlaneAddress, *allowedOrigins, *config, *componentsPath,
		*appProtocol, *mode, daprHTTP, daprInternalGRPC, daprAPIGRPC, applicationPort, profPort, *enableProfiling, *maxConcurrency, *enableMTLS, *sentryAddress)

	var globalConfig *global_config.Configuration
	var configErr error

	if *enableMTLS {
		runtimeConfig.CertChain, err = security.GetCertChain()
		if err != nil {
			return nil, err
		}
	}

	if *config != "" {
		switch modes.DaprMode(*mode) {
		case modes.KubernetesMode:
			client, conn, clientErr := client.GetOperatorClient(*controlPlaneAddress, security.TLSServerName, runtimeConfig.CertChain)
			if clientErr != nil {
				return nil, clientErr
			}
			defer conn.Close()

			globalConfig, configErr = global_config.LoadKubernetesConfiguration(*config, os.Getenv("NAMESPACE"), client)
		case modes.StandaloneMode:
			globalConfig, configErr = global_config.LoadStandaloneConfiguration(*config)
		}
	}

	if configErr != nil {
		log.Warnf("error loading configuration: %s", err)
	}
	if globalConfig == nil {
		log.Info("loading default configuration")
		globalConfig = global_config.LoadDefaultConfiguration()
	}
	return NewDaprRuntime(runtimeConfig, globalConfig), nil
}
