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

package options

import (
	// Register all components
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/dapr/pkg/validation"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.runtime.options")

type Options struct {
	Mode                         modes.DaprMode
	DaprHTTPPort                 int
	DaprAPIListenAddressList     []string
	DaprPublicPort               *int
	DaprAPIGRPCPort              int
	DaprInternalGRPCPort         int
	AppPort                      int
	ProfilePort                  int
	AppProtocol                  runtime.Protocol
	ResourcesPaths               []string
	ConfigPath                   string
	AppID                        string
	ControlPlaneAddress          string
	SentryAddress                string
	AllowedOrigins               string
	EnableProfiling              bool
	AppMaxConcurrency            int
	EnableMTLS                   bool
	AppSSL                       bool
	DaprHTTPMaxRequestSize       int
	UnixDomainSocket             string
	DaprHTTPReadBufferSize       int
	PlacementAddresses           []string
	GracefulShutdownDuration     time.Duration
	EnableAPILogging             *bool
	DisableBuiltinK8sSecretStore bool
	AppHealthCheck               *apphealth.Config
	AppHealthCheckPath           string
	AppChannelAddress            string

	Logger  logger.Options
	Metrics *metrics.Options
}

type flagSet struct {
	appID                        string
	componentsPath               string
	controlPlaneAddress          string
	sentryAddress                string
	allowedOrigins               string
	enableProfiling              bool
	appMaxConcurrency            int
	enableMTLS                   bool
	appSSL                       bool
	daprHTTPMaxRequestSize       int
	resourcesPath                stringSliceFlag
	appProtocol                  string
	enableAPILogging             bool
	runtimeVersion               bool
	buildInfo                    bool
	waitCommand                  bool
	daprHTTPPort                 string
	daprAPIGRPCPort              string
	profilePort                  string
	daprInternalGRPCPort         string
	daprPublicPort               string
	appPort                      string
	daprGracefulShutdownSeconds  int
	placementServiceHostAddr     string
	daprAPIListenAddresses       string
	appHealthProbeInterval       int
	appHealthProbeTimeout        int
	appHealthThreshold           int
	enableAppHealthCheck         bool
	mode                         string
	configPath                   string
	unixDomainSocket             string
	daprHTTPReadBufferSize       int
	disableBuiltinK8sSecretStore bool
	appHealthCheckPath           string
	appChannelAddress            string
	logger                       logger.Options
	metrics                      *metrics.Options
}

func New() (*Options, error) {
	var fs flagSet

	flag.StringVar(&fs.mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	flag.StringVar(&fs.daprHTTPPort, "dapr-http-port", strconv.Itoa(runtime.DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	flag.StringVar(&fs.daprAPIListenAddresses, "dapr-listen-addresses", runtime.DefaultAPIListenAddress, "One or more addresses for the Dapr API to listen on, CSV limited")
	flag.StringVar(&fs.daprPublicPort, "dapr-public-port", "", "Public port for Dapr Health and Metadata to listen on")
	flag.StringVar(&fs.daprAPIGRPCPort, "dapr-grpc-port", strconv.Itoa(runtime.DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	flag.StringVar(&fs.daprInternalGRPCPort, "dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	flag.StringVar(&fs.appPort, "app-port", "", "The port the application is listening on")
	flag.StringVar(&fs.profilePort, "profile-port", strconv.Itoa(runtime.DefaultProfilePort), "The port for the profile server")
	flag.StringVar(&fs.appProtocol, "app-protocol", string(runtime.HTTPProtocol), "Protocol for the application: grpc, grpcs, http, https, h2c")
	flag.StringVar(&fs.componentsPath, "components-path", "", "Alias for --resources-path [Deprecated, use --resources-path]")
	flag.Var(&fs.resourcesPath, "resources-path", "Path for resources directory. If not specified, no resources will be loaded. Can be passed multiple times")
	flag.StringVar(&fs.configPath, "config", "", "Path to config file, or name of a configuration object")
	flag.StringVar(&fs.appID, "app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	flag.StringVar(&fs.controlPlaneAddress, "control-plane-address", "", "Address for a Dapr control plane")
	flag.StringVar(&fs.sentryAddress, "sentry-address", "", "Address for the Sentry CA service")
	flag.StringVar(&fs.placementServiceHostAddr, "placement-host-address", "", "Addresses for Dapr Actor Placement servers")
	flag.StringVar(&fs.allowedOrigins, "allowed-origins", cors.DefaultAllowedOrigins, "Allowed HTTP origins")
	flag.BoolVar(&fs.enableProfiling, "enable-profiling", false, "Enable profiling")
	flag.BoolVar(&fs.runtimeVersion, "version", false, "Prints the runtime version")
	flag.BoolVar(&fs.buildInfo, "build-info", false, "Prints the build info")
	flag.BoolVar(&fs.waitCommand, "wait", false, "wait for Dapr outbound ready")
	flag.IntVar(&fs.appMaxConcurrency, "app-max-concurrency", -1, "Controls the concurrency level when forwarding requests to user code; set to -1 for no limits")
	flag.BoolVar(&fs.enableMTLS, "enable-mtls", false, "Enables automatic mTLS for daprd to daprd communication channels")
	flag.BoolVar(&fs.appSSL, "app-ssl", false, "Sets the URI scheme of the app to https and attempts a TLS connection [Deprecated, use '--app-protocol https|grpcs']")
	flag.IntVar(&fs.daprHTTPMaxRequestSize, "dapr-http-max-request-size", runtime.DefaultMaxRequestBodySize, "Increasing max size of request body in MB to handle uploading of big files")
	flag.StringVar(&fs.unixDomainSocket, "unix-domain-socket", "", "Path to a unix domain socket dir mount. If specified, Dapr API servers will use Unix Domain Sockets")
	flag.IntVar(&fs.daprHTTPReadBufferSize, "dapr-http-read-buffer-size", runtime.DefaultReadBufferSize, "Increasing max size of read buffer in KB to handle sending multi-KB headers")
	flag.IntVar(&fs.daprGracefulShutdownSeconds, "dapr-graceful-shutdown-seconds", int(runtime.DefaultGracefulShutdownDuration/time.Second), "Graceful shutdown time in seconds")
	flag.BoolVar(&fs.enableAPILogging, "enable-api-logging", false, "Enable API logging for API calls")
	flag.BoolVar(&fs.disableBuiltinK8sSecretStore, "disable-builtin-k8s-secret-store", false, "Disable the built-in Kubernetes Secret Store")
	flag.BoolVar(&fs.enableAppHealthCheck, "enable-app-health-check", false, "Enable health checks for the application using the protocol defined with app-protocol")
	flag.StringVar(&fs.appHealthCheckPath, "app-health-check-path", runtime.DefaultAppHealthCheckPath, "Path used for health checks; HTTP only")
	flag.IntVar(&fs.appHealthProbeInterval, "app-health-probe-interval", int(apphealth.DefaultProbeInterval/time.Second), "Interval to probe for the health of the app in seconds")
	flag.IntVar(&fs.appHealthProbeTimeout, "app-health-probe-timeout", int(apphealth.DefaultProbeTimeout/time.Millisecond), "Timeout for app health probes in milliseconds")
	flag.IntVar(&fs.appHealthThreshold, "app-health-threshold", int(apphealth.DefaultThreshold), "Number of consecutive failures for the app to be considered unhealthy")
	flag.StringVar(&fs.appChannelAddress, "app-channel-address", runtime.DefaultChannelAddress, "The network address the application listens on")

	fs.logger = logger.DefaultOptions()
	fs.logger.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	fs.metrics = metrics.DefaultMetricOptions()
	fs.metrics.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	// Ignore errors; CommandLine is set for ExitOnError.
	flagCommandLine.Parse()

	if fs.runtimeVersion {
		fmt.Println(buildinfo.Version())
		os.Exit(0)
	}

	if fs.buildInfo {
		fmt.Printf("Version: %s\nGit Commit: %s\nGit Version: %s\n", buildinfo.Version(), buildinfo.Commit(), buildinfo.GitVersion())
		os.Exit(0)
	}

	if fs.waitCommand {
		runtime.WaitUntilDaprOutboundReady(fs.daprHTTPPort)
		os.Exit(0)
	}

	return fs.parseValues()
}

func (f flagSet) parseValues(args []string) (*Options, error) {
	opts := Options{
		AppID:                        f.appID,
		Mode:                         modes.DaprMode(f.mode),
		ConfigPath:                   f.configPath,
		SentryAddress:                f.sentryAddress,
		AllowedOrigins:               f.allowedOrigins,
		ControlPlaneAddress:          f.controlPlaneAddress,
		EnableProfiling:              f.enableProfiling,
		EnableMTLS:                   f.enableMTLS,
		AppChannelAddress:            f.appChannelAddress,
		AppHealthCheckPath:           f.appHealthCheckPath,
		DisableBuiltinK8sSecretStore: f.disableBuiltinK8sSecretStore,
		AppSSL:                       f.appSSL,
		UnixDomainSocket:             f.unixDomainSocket,
		AppMaxConcurrency:            f.appMaxConcurrency,
		DaprHTTPMaxRequestSize:       f.daprHTTPMaxRequestSize,
		DaprHTTPReadBufferSize:       f.daprHTTPReadBufferSize,
		Logger:                       f.logger,
		Metrics:                      f.metrics,
	}

	// flag.Parse() will always set a value to "enableAPILogging", and it will be false whether it's explicitly set to false or unset
	// For this flag, we need the third state (unset) so we need to do a bit more work here to check if it's unset, then mark "enableAPILogging" as nil
	// It's not the prettiest approach, but…
	if f.enableAPILogging {
		opts.EnableAPILogging = ptr.Of(true)
	} else {
		opts.EnableAPILogging = nil
		for _, v := range args {
			if strings.HasPrefix(v, "--enable-api-logging") || strings.HasPrefix(v, "-enable-api-logging") {
				// This means that enable-api-logging was explicitly set to false
				opts.EnableAPILogging = ptr.Of(false)
				break
			}
		}
	}

	if len(f.resourcesPath) == 0 && f.componentsPath != "" {
		f.resourcesPath = stringSliceFlag{f.componentsPath}
	}
	opts.ResourcesPaths = f.resourcesPath
	if opts.Mode == modes.StandaloneMode {
		if err := validation.ValidateSelfHostedAppID(f.appID); err != nil {
			return nil, err
		}
	}

	// Apply options to all loggers.
	opts.Logger.SetAppID(opts.AppID)

	var err error
	opts.DaprHTTPPort, err = strconv.Atoi(f.daprHTTPPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-http-port flag: %w", err)
	}

	opts.DaprAPIGRPCPort, err = strconv.Atoi(f.daprAPIGRPCPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing dapr-grpc-port flag: %w", err)
	}

	opts.ProfilePort, err = strconv.Atoi(f.profilePort)
	if err != nil {
		return nil, fmt.Errorf("error parsing profile-port flag: %w", err)
	}

	if f.daprInternalGRPCPort != "" && f.daprInternalGRPCPort != "0" {
		opts.DaprInternalGRPCPort, err = strconv.Atoi(f.daprInternalGRPCPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing dapr-internal-grpc-port: %w", err)
		}
	} else {
		// Get a "stable random" port in the range 47300-49,347 if it can be
		// acquired using a deterministic algorithm that returns the same value if
		// the same app is restarted
		// Otherwise, the port will be random.
		opts.DaprInternalGRPCPort, err = utils.GetStablePort(47300, opts.AppID)
		if err != nil {
			return nil, fmt.Errorf("failed to get free port for internal grpc server: %w", err)
		}
	}

	if f.daprPublicPort != "" {
		port, err := strconv.Atoi(f.daprPublicPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing dapr-public-port: %w", err)
		}
		opts.DaprPublicPort = &port
	}

	if f.appPort != "" {
		opts.AppPort, err = strconv.Atoi(f.appPort)
		if err != nil {
			return nil, fmt.Errorf("error parsing app-port: %w", err)
		}
	}

	if opts.AppPort == opts.DaprHTTPPort {
		return nil, fmt.Errorf("the 'dapr-http-port' argument value %d conflicts with 'app-port'", opts.DaprHTTPPort)
	}

	if opts.AppPort == opts.DaprAPIGRPCPort {
		return nil, fmt.Errorf("the 'dapr-grpc-port' argument value %d conflicts with 'app-port'", opts.DaprAPIGRPCPort)
	}

	if opts.DaprHTTPMaxRequestSize == -1 {
		opts.DaprHTTPMaxRequestSize = runtime.DefaultMaxRequestBodySize
	}

	if opts.DaprHTTPReadBufferSize == -1 {
		opts.DaprHTTPReadBufferSize = runtime.DefaultReadBufferSize
	}

	if f.daprGracefulShutdownSeconds < 0 {
		opts.GracefulShutdownDuration = runtime.DefaultGracefulShutdownDuration
	} else {
		opts.GracefulShutdownDuration = time.Duration(f.daprGracefulShutdownSeconds) * time.Second
	}

	if f.placementServiceHostAddr != "" {
		opts.PlacementAddresses = parsePlacementAddr(f.placementServiceHostAddr)
	}

	if opts.AppMaxConcurrency == -1 {
		opts.AppMaxConcurrency = 0
	}

	switch p := strings.ToLower(f.appProtocol); p {
	case string(runtime.GRPCSProtocol):
		opts.AppProtocol = runtime.GRPCSProtocol
	case string(runtime.HTTPSProtocol):
		opts.AppProtocol = runtime.HTTPSProtocol
	case string(runtime.H2CProtocol):
		opts.AppProtocol = runtime.H2CProtocol
	case string(runtime.HTTPProtocol):
		// For backwards compatibility, when protocol is HTTP and --app-ssl is set, use "https"
		// TODO: Remove in a future Dapr version
		if opts.AppSSL {
			log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=https' instead")
			opts.AppProtocol = runtime.HTTPSProtocol
		} else {
			opts.AppProtocol = runtime.HTTPProtocol
		}
	case string(runtime.GRPCProtocol):
		// For backwards compatibility, when protocol is GRPC and --app-ssl is set, use "grpcs"
		// TODO: Remove in a future Dapr version
		if opts.AppSSL {
			log.Warn("The 'app-ssl' flag is deprecated; use 'app-protocol=grpcs' instead")
			opts.AppProtocol = runtime.GRPCSProtocol
		} else {
			opts.AppProtocol = runtime.GRPCProtocol
		}
	case "":
		opts.AppProtocol = runtime.HTTPProtocol
	default:
		return nil, fmt.Errorf("invalid value for 'app-protocol': %v", f.appProtocol)
	}

	opts.DaprAPIListenAddressList = strings.Split(f.daprAPIListenAddresses, ",")
	if len(opts.DaprAPIListenAddressList) == 0 {
		opts.DaprAPIListenAddressList = []string{runtime.DefaultAPIListenAddress}
	}

	healthProbeInterval := time.Duration(f.appHealthProbeInterval) * time.Second
	if f.appHealthProbeInterval <= 0 {
		healthProbeInterval = apphealth.DefaultProbeInterval
	}

	healthProbeTimeout := time.Duration(f.appHealthProbeTimeout) * time.Millisecond
	if f.appHealthProbeTimeout <= 0 {
		healthProbeTimeout = apphealth.DefaultProbeTimeout
	}

	if healthProbeTimeout > healthProbeInterval {
		return nil, errors.New("value for 'health-probe-timeout' must be smaller than 'health-probe-interval'")
	}

	// Also check to ensure no overflow with int32
	healthThreshold := int32(f.appHealthThreshold)
	if f.appHealthThreshold < 1 || int32(f.appHealthThreshold+1) < 0 {
		healthThreshold = apphealth.DefaultThreshold
	}

	if f.enableAppHealthCheck {
		opts.AppHealthCheck = &apphealth.Config{
			ProbeInterval: healthProbeInterval,
			ProbeTimeout:  healthProbeTimeout,
			ProbeOnly:     true,
			Threshold:     healthThreshold,
		}
	}

	return &opts, nil
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
