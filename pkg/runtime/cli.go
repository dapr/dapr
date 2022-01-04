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
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/acl"
	global_config "github.com/dapr/dapr/pkg/config"
	env "github.com/dapr/dapr/pkg/config/env"
	"github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/grpc"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/client"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
)

// make it easier for test.
var (
	mode                      = flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	daprHTTPPort              = flag.String("dapr-http-port", fmt.Sprintf("%v", DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	daprAPIListenAddresses    = flag.String("dapr-listen-addresses", DefaultAPIListenAddress, "One or more addresses for the Dapr API to listen on, CSV limited")
	daprPublicPort            = flag.String("dapr-public-port", "", "Public port for Dapr Health and Metadata to listen on")
	daprAPIGRPCPort           = flag.String("dapr-grpc-port", fmt.Sprintf("%v", DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	daprInternalGRPCPort      = flag.String("dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	appPort                   = flag.String("app-port", "", "The port the application is listening on")
	profilePort               = flag.String("profile-port", fmt.Sprintf("%v", DefaultProfilePort), "The port for the profile server")
	appProtocol               = flag.String("app-protocol", string(HTTPProtocol), "Protocol for the application: grpc or http")
	componentsPath            = flag.String("components-path", "", "Path for components directory. If empty, components will not be loaded. Self-hosted mode only")
	daprConfig                = flag.String("config", "", "Path to config file, or name of a configuration object")
	appID                     = flag.String("app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	controlPlaneAddress       = flag.String("control-plane-address", "", "Address for a Dapr control plane")
	sentryAddress             = flag.String("sentry-address", "", "Address for the Sentry CA service")
	placementServiceHostAddr  = flag.String("placement-host-address", "", "Addresses for Dapr Actor Placement servers")
	allowedOrigins            = flag.String("allowed-origins", cors.DefaultAllowedOrigins, "Allowed HTTP origins")
	enableProfiling           = flag.Bool("enable-profiling", false, "Enable profiling")
	runtimeVersion            = flag.Bool("version", false, "Prints the runtime version")
	buildInfo                 = flag.Bool("build-info", false, "Prints the build info")
	waitCommand               = flag.Bool("wait", false, "wait for Dapr outbound ready")
	appMaxConcurrency         = flag.Int("app-max-concurrency", -1, "Controls the concurrency level when forwarding requests to user code")
	enableMTLS                = flag.Bool("enable-mtls", false, "Enables automatic mTLS for daprd to daprd communication channels")
	appSSL                    = flag.Bool("app-ssl", false, "Sets the URI scheme of the app to https and attempts an SSL connection")
	daprHTTPMaxRequestSize    = flag.Int("dapr-http-max-request-size", -1, "Increasing max size of request body in MB to handle uploading of big files. By default 4 MB.")
	unixDomainSocket          = flag.String("unix-domain-socket", "", "Path to a unix domain socket dir mount. If specified, Dapr API servers will use Unix Domain Sockets")
	daprHTTPReadBufferSize    = flag.Int("dapr-http-read-buffer-size", -1, "Increasing max size of read buffer in KB to handle sending multi-KB headers. By default 4 KB.")
	daprHTTPStreamRequestBody = flag.Bool("dapr-http-stream-request-body", false, "Enables request body streaming on http server")
	daprGracefulShutdownSeconds = flag.Int("dapr-graceful-shutdown-seconds", -1, "Graceful shutdown time in seconds.")
)

// FromFlags parses command flags and returns DaprRuntime instance.
func FromFlags() (*DaprRuntime, error) {
	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)

	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

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
	if *daprGracefulShutdownSeconds == -1 {
		gracefulShutdownDuration = defaultGracefulShutdownDuration
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

	var (
		certChain    *credentials.CertChain
		globalConfig *global_config.Configuration
		configErr    error
		namespace    string
	)

	if *daprConfig != "" {
		switch modes.DaprMode(*mode) {
		case modes.KubernetesMode:
			certChain, err = security.GetCertChain()
			if err != nil {
				return nil, err
			}
			client, conn, clientErr := client.GetOperatorClient(*controlPlaneAddress, security.TLSServerName, certChain)
			if clientErr != nil {
				return nil, clientErr
			}
			defer conn.Close()
			namespace = os.Getenv("NAMESPACE")
			globalConfig, configErr = global_config.LoadKubernetesConfiguration(*daprConfig, namespace, client)
		case modes.StandaloneMode:
			globalConfig, _, configErr = global_config.LoadStandaloneConfiguration(*daprConfig)
		}
	}

	if configErr != nil {
		log.Fatalf("error loading configuration: %s", configErr)
	}
	if globalConfig == nil {
		log.Info("loading default configuration")
		globalConfig = global_config.LoadDefaultConfiguration()
	}

	// if enableMTLS or sentryAddress is not set from flag, then set it from config
	if !*enableMTLS {
		*enableMTLS = globalConfig.Spec.MTLSSpec.Enabled
	}
	if *sentryAddress == "" {
		*sentryAddress = globalConfig.Spec.MTLSSpec.SentryAddress
	}

	// if tls is enabled but certChain hasn't load, then load it
	if *enableMTLS && certChain == nil {
		certChain, err = security.GetCertChain()
		if err != nil {
			return nil, err
		}
	}

	runtimeConfig := NewRuntimeConfig(*appID, placementAddresses, *controlPlaneAddress, *allowedOrigins, *daprConfig, *componentsPath,
		appPrtcl, *mode, daprHTTP, daprInternalGRPC, daprAPIGRPC, daprAPIListenAddressList, publicPort, applicationPort, profPort, *enableProfiling, concurrency, *enableMTLS, *sentryAddress, *appSSL, maxRequestBodySize, *unixDomainSocket, readBufferSize, *daprHTTPStreamRequestBody, gracefulShutdownDuration)
	runtimeConfig.CertChain = certChain

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

	if err = setEnvVariables(variables); err != nil {
		return nil, err
	}

	var accessControlList *global_config.AccessControlList
	accessControlList, err = acl.ParseAccessControlSpec(globalConfig.Spec.AccessControlSpec, string(runtimeConfig.ApplicationProtocol))
	if err != nil {
		log.Fatalf(err.Error())
	}
	return NewDaprRuntime(runtimeConfig, globalConfig, accessControlList), nil
}

func setEnvVariables(variables map[string]string) error {
	for key, value := range variables {
		err := os.Setenv(key, value)
		if err != nil {
			return err
		}
	}
	return nil
}

func parsePlacementAddr(val string) []string {
	parsed := []string{}
	p := strings.Split(val, ",")
	for _, addr := range p {
		parsed = append(parsed, strings.TrimSpace(addr))
	}
	return parsed
}
