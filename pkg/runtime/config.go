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
	"time"

	"github.com/dapr/dapr/pkg/apphealth"
	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/modes"
)

// Protocol is a communications protocol.
type Protocol string

const (
	// GRPCProtocol is a gRPC communication protocol.
	GRPCProtocol Protocol = "grpc"
	// HTTPProtocol is a HTTP communication protocol.
	HTTPProtocol Protocol = "http"
	// WASMProtocol is a WASM communication protocol.
	WASMProtocol Protocol = "wasm"
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
	DefaultAppHealthCheckPath = "/health"
)

// Config holds the Dapr Runtime configuration.
type Config struct {
	ID                           string
	HTTPPort                     int
	PublicPort                   *int
	ProfilePort                  int
	EnableProfiling              bool
	APIGRPCPort                  int
	InternalGRPCPort             int
	ApplicationPort              int
	APIListenAddresses           []string
	ApplicationProtocol          Protocol
	Mode                         modes.DaprMode
	PlacementAddresses           []string
	GlobalConfig                 string
	AllowedOrigins               string
	Standalone                   config.StandaloneConfig
	Kubernetes                   config.KubernetesConfig
	MaxConcurrency               int
	mtlsEnabled                  bool
	SentryServiceAddress         string
	CertChain                    *credentials.CertChain
	AppSSL                       bool
	MaxRequestBodySize           int
	UnixDomainSocket             string
	ReadBufferSize               int
	GracefulShutdownDuration     time.Duration
	EnableAPILogging             bool
	DisableBuiltinK8sSecretStore bool
	AppHealthCheck               *apphealth.Config
	AppHealthCheckHTTPPath       string
}

// NewRuntimeConfigOpts contains options for NewRuntimeConfig.
type NewRuntimeConfigOpts struct {
	ID                           string
	PlacementAddresses           []string
	controlPlaneAddress          string
	AllowedOrigins               string
	GlobalConfig                 string
	ComponentsPath               string
	AppProtocol                  string
	Mode                         string
	HTTPPort                     int
	InternalGRPCPort             int
	APIGRPCPort                  int
	APIListenAddresses           []string
	PublicPort                   *int
	AppPort                      int
	ProfilePort                  int
	EnableProfiling              bool
	MaxConcurrency               int
	MTLSEnabled                  bool
	SentryAddress                string
	AppSSL                       bool
	MaxRequestBodySize           int
	UnixDomainSocket             string
	ReadBufferSize               int
	GracefulShutdownDuration     time.Duration
	EnableAPILogging             bool
	DisableBuiltinK8sSecretStore bool
	EnableAppHealthCheck         bool
	AppHealthCheckPath           string
	AppHealthProbeInterval       time.Duration
	AppHealthProbeTimeout        time.Duration
	AppHealthThreshold           int32
}

// NewRuntimeConfig returns a new runtime config.
func NewRuntimeConfig(opts NewRuntimeConfigOpts) *Config {
	var appHealthCheck *apphealth.Config
	if opts.EnableAppHealthCheck {
		appHealthCheck = &apphealth.Config{
			ProbeInterval: opts.AppHealthProbeInterval,
			ProbeTimeout:  opts.AppHealthProbeTimeout,
			ProbeOnly:     true,
			Threshold:     opts.AppHealthThreshold,
		}
	}

	return &Config{
		ID:                  opts.ID,
		HTTPPort:            opts.HTTPPort,
		PublicPort:          opts.PublicPort,
		InternalGRPCPort:    opts.InternalGRPCPort,
		APIGRPCPort:         opts.APIGRPCPort,
		ApplicationPort:     opts.AppPort,
		ProfilePort:         opts.ProfilePort,
		APIListenAddresses:  opts.APIListenAddresses,
		ApplicationProtocol: Protocol(opts.AppProtocol),
		Mode:                modes.DaprMode(opts.Mode),
		PlacementAddresses:  opts.PlacementAddresses,
		GlobalConfig:        opts.GlobalConfig,
		AllowedOrigins:      opts.AllowedOrigins,
		Standalone: config.StandaloneConfig{
			ComponentsPath: opts.ComponentsPath,
		},
		Kubernetes: config.KubernetesConfig{
			ControlPlaneAddress: opts.controlPlaneAddress,
		},
		EnableProfiling:              opts.EnableProfiling,
		MaxConcurrency:               opts.MaxConcurrency,
		mtlsEnabled:                  opts.MTLSEnabled,
		SentryServiceAddress:         opts.SentryAddress,
		AppSSL:                       opts.AppSSL,
		MaxRequestBodySize:           opts.MaxRequestBodySize,
		UnixDomainSocket:             opts.UnixDomainSocket,
		ReadBufferSize:               opts.ReadBufferSize,
		GracefulShutdownDuration:     opts.GracefulShutdownDuration,
		EnableAPILogging:             opts.EnableAPILogging,
		DisableBuiltinK8sSecretStore: opts.DisableBuiltinK8sSecretStore,
		AppHealthCheck:               appHealthCheck,
		AppHealthCheckHTTPPath:       opts.AppHealthCheckPath,
	}
}
