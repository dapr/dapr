// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	global_config "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/version"
	"github.com/pkg/errors"
)

// FromFlags parses command flags and returns DaprRuntime instance
func FromFlags() (*DaprRuntime, error) {
	mode := flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	daprHTTPPort := flag.String("dapr-http-port", fmt.Sprintf("%v", DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	daprAPIGRPCPort := flag.String("dapr-grpc-port", fmt.Sprintf("%v", DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	daprInternalGRPCPort := flag.String("dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	appPort := flag.String("app-port", "", "The port the application is listening on")
	profilePort := flag.String("profile-port", fmt.Sprintf("%v", DefaultProfilePort), "The port for the profile server")
	appProtocol := flag.String("app-protocol", string(HTTPProtocol), "Protocol for the application: grpc or http")
	componentsPath := flag.String("components-path", "", "Path for components directory. If empty, components will not be loaded. Self-hosted mode only")
	config := flag.String("config", "", "Path to config file, or name of a configuration object")
	appID := flag.String("app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	controlPlaneAddress := flag.String("control-plane-address", "", "Address for a Dapr control plane")
	sentryAddress := flag.String("sentry-address", "", "Address for the Sentry CA service")
	placementServiceHostAddr := flag.String("placement-host-address", "", "Addresses for Dapr Actor Placement servers")
	allowedOrigins := flag.String("allowed-origins", cors.DefaultAllowedOrigins, "Allowed HTTP origins")
	enableProfiling := flag.Bool("enable-profiling", false, "Enable profiling")
	runtimeVersion := flag.Bool("version", false, "Prints the runtime version")
	appMaxConcurrency := flag.Int("app-max-concurrency", -1, "Controls the concurrency level when forwarding requests to user code")
	enableMTLS := flag.Bool("enable-mtls", false, "Enables automatic mTLS for daprd to daprd communication channels")
	appSSL := flag.Bool("app-ssl", false, "Sets the URI scheme of the app to https and attempts an SSL connection")
	daprHTTPMaxRequestSize := flag.Int("dapr-http-max-request-size", -1, "Increasing max size of request body in MB to handle uploading of big files. By default 4 MB.")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)

	// attaching only metrics-port option
	metricsExporter.Options().AttachCmdFlag(flag.StringVar)

	flag.Parse()

	if *runtimeVersion {
		fmt.Println(version.Version())
		os.Exit(0)
	}

	if *appID == "" {
		return nil, errors.New("app-id parameter cannot be empty")
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
	if *daprInternalGRPCPort != "" {
		daprInternalGRPC, err = strconv.Atoi(*daprInternalGRPCPort)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing dapr-internal-grpc-port")
		}
	} else {
		daprInternalGRPC, err = grpc.GetFreePort()
		if err != nil {
			return nil, errors.Wrap(err, "failed to get free port for internal grpc server")
		}
	}

	var applicationPort int
	if *appPort != "" {
		applicationPort, err = strconv.Atoi(*appPort)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing app-port")
		}
	}

	var maxRequestBodySize int
	if *daprHTTPMaxRequestSize != -1 {
		maxRequestBodySize = *daprHTTPMaxRequestSize
	} else {
		maxRequestBodySize = DefaultMaxRequestBodySize
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

	runtimeConfig := NewRuntimeConfig(*appID, placementAddresses, *controlPlaneAddress, *allowedOrigins, *config, *componentsPath,
		appPrtcl, *mode, daprHTTP, daprInternalGRPC, daprAPIGRPC, applicationPort, profPort, *enableProfiling, concurrency, *enableMTLS, *sentryAddress, *appSSL, maxRequestBodySize)

	var globalConfig *global_config.Configuration
	var configErr error

	if *enableMTLS {
		runtimeConfig.CertChain, err = security.GetCertChain()
		if err != nil {
			return nil, err
		}
	}

	var accessControlList *global_config.AccessControlList
	var namespace string

	if *config != "" {
		switch modes.DaprMode(*mode) {
		case modes.KubernetesMode:
			client, conn, clientErr := client.GetOperatorClient(*controlPlaneAddress, security.TLSServerName, runtimeConfig.CertChain)
			if clientErr != nil {
				return nil, clientErr
			}
			defer conn.Close()
			namespace = os.Getenv("NAMESPACE")
			globalConfig, configErr = global_config.LoadKubernetesConfiguration(*config, namespace, client)
		case modes.DNSMode:
			globalConfig, _, configErr = global_config.LoadStandaloneConfiguration(*config)
		case modes.StandaloneMode:
			globalConfig, _, configErr = global_config.LoadStandaloneConfiguration(*config)
		}

		if configErr != nil {
			log.Debugf("Config error: %v", configErr)
		}
	}

	if configErr != nil {
		log.Fatalf("error loading configuration: %s", configErr)
	}
	if globalConfig == nil {
		log.Info("loading default configuration")
		globalConfig = global_config.LoadDefaultConfiguration()
	}

	accessControlList, err = global_config.ParseAccessControlSpec(globalConfig.Spec.AccessControlSpec, string(runtimeConfig.ApplicationProtocol))
	if err != nil {
		log.Fatalf(err.Error())
	}
	return NewDaprRuntime(runtimeConfig, globalConfig, accessControlList), nil
}

func parsePlacementAddr(val string) []string {
	parsed := []string{}
	p := strings.Split(val, ",")
	for _, addr := range p {
		parsed = append(parsed, strings.TrimSpace(addr))
	}
	return parsed
}
