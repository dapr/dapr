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
	"os"
	"strconv"
	"time"

	"github.com/spf13/pflag"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	"github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID                        string
	ComponentsPath               string
	ControlPlaneAddress          string
	ControlPlaneTrustDomain      string
	ControlPlaneNamespace        string
	SentryAddress                string
	TrustAnchors                 []byte
	AllowedOrigins               string
	EnableProfiling              bool
	AppMaxConcurrency            int
	EnableMTLS                   bool
	AppSSL                       bool
	DaprHTTPMaxRequestSize       int
	ResourcesPath                []string
	AppProtocol                  string
	EnableAPILogging             *bool
	RuntimeVersion               bool
	BuildInfo                    bool
	WaitCommand                  bool
	DaprHTTPPort                 string
	DaprAPIGRPCPort              string
	ProfilePort                  string
	DaprInternalGRPCPort         string
	DaprPublicPort               string
	AppPort                      string
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
	Logger                       logger.Options
	Metrics                      *metrics.Options
}

func New(origArgs []string) *Options {
	opts := Options{
		EnableAPILogging:          new(bool),
		DaprBlockShutdownDuration: new(time.Duration),
	}

	// We are using pflag to parse the CLI flags
	// pflag is a drop-in replacement for the standard library's "flag" package, however…
	// There's one key difference: with the stdlib's "flag" package, there are no short-hand options so options can be defined with a single slash (such as "daprd -mode").
	// With pflag, single slashes are reserved for shorthands.
	// So, we are doing this thing where we iterate through all args and double-up the slash if it's single
	// This works *as long as* we don't start using shorthand flags (which haven't been in use so far).
	args := make([]string, len(origArgs))
	for i, a := range origArgs {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			args[i] = "-" + a
		} else {
			args[i] = a
		}
	}

	// Create a flag set
	fs := pflag.NewFlagSet("daprd", pflag.ExitOnError)
	fs.SortFlags = true

	fs.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	fs.StringVar(&opts.DaprHTTPPort, "dapr-http-port", strconv.Itoa(runtime.DefaultDaprHTTPPort), "HTTP port for Dapr API to listen on")
	fs.StringVar(&opts.DaprAPIListenAddresses, "dapr-listen-addresses", runtime.DefaultAPIListenAddress, "One or more addresses for the Dapr API to listen on, CSV limited")
	fs.StringVar(&opts.DaprPublicPort, "dapr-public-port", "", "Public port for Dapr Health and Metadata to listen on")
	fs.StringVar(&opts.DaprAPIGRPCPort, "dapr-grpc-port", strconv.Itoa(runtime.DefaultDaprAPIGRPCPort), "gRPC port for the Dapr API to listen on")
	fs.StringVar(&opts.DaprInternalGRPCPort, "dapr-internal-grpc-port", "", "gRPC port for the Dapr Internal API to listen on")
	fs.StringVar(&opts.AppPort, "app-port", "", "The port the application is listening on")
	fs.StringVar(&opts.ProfilePort, "profile-port", strconv.Itoa(runtime.DefaultProfilePort), "The port for the profile server")
	fs.StringVar(&opts.AppProtocol, "app-protocol", string(protocol.HTTPProtocol), "Protocol for the application: grpc, grpcs, http, https, h2c")
	fs.StringVar(&opts.ComponentsPath, "components-path", "", "Alias for --resources-path")
	fs.MarkDeprecated("components-path", "use --resources-path")
	fs.StringSliceVar(&opts.ResourcesPath, "resources-path", nil, "Path for resources directory. If not specified, no resources will be loaded. Can be passed multiple times")
	fs.StringSliceVar(&opts.Config, "config", nil, "Path to config file, or name of a configuration object. In standalone mode, can be passed multiple times")
	fs.StringVar(&opts.AppID, "app-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	fs.StringVar(&opts.ControlPlaneAddress, "control-plane-address", "", "Address for a Dapr control plane")
	fs.StringVar(&opts.SentryAddress, "sentry-address", "", "Address for the Sentry CA service")
	fs.StringVar(&opts.ControlPlaneTrustDomain, "control-plane-trust-domain", "localhost", "Trust domain of the Dapr control plane")
	fs.StringVar(&opts.ControlPlaneNamespace, "control-plane-namespace", "default", "Namespace of the Dapr control plane")
	fs.StringVar(&opts.AllowedOrigins, "allowed-origins", cors.DefaultAllowedOrigins, "Allowed HTTP origins")
	fs.BoolVar(&opts.EnableProfiling, "enable-profiling", false, "Enable profiling")
	fs.BoolVar(&opts.RuntimeVersion, "version", false, "Prints the runtime version")
	fs.BoolVar(&opts.BuildInfo, "build-info", false, "Prints the build info")
	fs.BoolVar(&opts.WaitCommand, "wait", false, "wait for Dapr outbound ready")
	fs.IntVar(&opts.AppMaxConcurrency, "app-max-concurrency", -1, "Controls the concurrency level when forwarding requests to user code; set to -1 for no limits")
	fs.BoolVar(&opts.EnableMTLS, "enable-mtls", false, "Enables automatic mTLS for daprd-to-daprd communication channels")
	fs.BoolVar(&opts.AppSSL, "app-ssl", false, "Sets the URI scheme of the app to https and attempts a TLS connection")
	fs.MarkDeprecated("app-ssl", "use '--app-protocol https|grpcs'")
	fs.IntVar(&opts.DaprHTTPMaxRequestSize, "dapr-http-max-request-size", runtime.DefaultMaxRequestBodySize, "Increasing max size of request body in MB to handle uploading of big files")
	fs.StringVar(&opts.UnixDomainSocket, "unix-domain-socket", "", "Path to a unix domain socket dir mount. If specified, Dapr API servers will use Unix Domain Sockets")
	fs.IntVar(&opts.DaprHTTPReadBufferSize, "dapr-http-read-buffer-size", runtime.DefaultReadBufferSize, "Increasing max size of read buffer in KB to handle sending multi-KB headers")
	fs.IntVar(&opts.DaprGracefulShutdownSeconds, "dapr-graceful-shutdown-seconds", int(runtime.DefaultGracefulShutdownDuration/time.Second), "Graceful shutdown time in seconds")
	fs.DurationVar(opts.DaprBlockShutdownDuration, "dapr-block-shutdown-duration", 0, "If enabled, will block graceful shutdown after terminate signal is received until either the given duration has elapsed or the app reports unhealthy. Disabled by default")
	fs.BoolVar(opts.EnableAPILogging, "enable-api-logging", false, "Enable API logging for API calls")
	fs.BoolVar(&opts.DisableBuiltinK8sSecretStore, "disable-builtin-k8s-secret-store", false, "Disable the built-in Kubernetes Secret Store")
	fs.BoolVar(&opts.EnableAppHealthCheck, "enable-app-health-check", false, "Enable health checks for the application using the protocol defined with app-protocol")
	fs.StringVar(&opts.AppHealthCheckPath, "app-health-check-path", runtime.DefaultAppHealthCheckPath, "Path used for health checks; HTTP only")
	fs.IntVar(&opts.AppHealthProbeInterval, "app-health-probe-interval", int(config.AppHealthConfigDefaultProbeInterval/time.Second), "Interval to probe for the health of the app in seconds")
	fs.IntVar(&opts.AppHealthProbeTimeout, "app-health-probe-timeout", int(config.AppHealthConfigDefaultProbeTimeout/time.Millisecond), "Timeout for app health probes in milliseconds")
	fs.IntVar(&opts.AppHealthThreshold, "app-health-threshold", int(config.AppHealthConfigDefaultThreshold), "Number of consecutive failures for the app to be considered unhealthy")
	fs.StringVar(&opts.AppChannelAddress, "app-channel-address", runtime.DefaultChannelAddress, "The network address the application listens on")

	// Add flags for actors, placement, and reminders
	// --placement-host-address is a legacy (but not deprecated) flag that is translated to the actors-service flag
	var placementServiceHostAddr string
	fs.StringVar(&placementServiceHostAddr, "placement-host-address", "", "Addresses for Dapr Actor Placement servers (overrides actors-service)")
	fs.StringVar(&opts.ActorsService, "actors-service", "", "Type and address of the actors service, in the format 'type:address'")
	fs.StringVar(&opts.RemindersService, "reminders-service", "", "Type and address of the reminders service, in the format 'type:address'")

	// Add flags for logger and metrics
	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	// Ignore errors; flagset is set for ExitOnError
	_ = fs.Parse(args)

	// flag.Parse() will always set a value to "enableAPILogging", and it will be false whether it's explicitly set to false or unset
	// For this flag, we need the third state (unset) so we need to do a bit more work here to check if it's unset, then mark "enableAPILogging" as nil
	if !*opts.EnableAPILogging && !fs.Changed("enable-api-logging") {
		opts.EnableAPILogging = nil
	}

	// If placement-host-address is set, that always takes priority over actors-service
	if placementServiceHostAddr != "" {
		opts.ActorsService = "placement:" + placementServiceHostAddr
	}

	opts.TrustAnchors = []byte(os.Getenv(consts.TrustAnchorsEnvVar))

	if !fs.Changed("control-plane-namespace") {
		ns, ok := os.LookupEnv(consts.ControlPlaneNamespaceEnvVar)
		if ok {
			opts.ControlPlaneNamespace = ns
		}
	}

	if !fs.Changed("control-plane-trust-domain") {
		td, ok := os.LookupEnv(consts.ControlPlaneTrustDomainEnvVar)
		if ok {
			opts.ControlPlaneTrustDomain = td
		}
	}

	if !fs.Changed("dapr-block-shutdown-duration") {
		opts.DaprBlockShutdownDuration = nil
	}

	return &opts
}
