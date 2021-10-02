// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
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
)

// Config holds the Dapr Runtime configuration.
type Config struct {
	ID                   string
	HTTPPort             int
	PublicPort           *int
	ProfilePort          int
	EnableProfiling      bool
	APIGRPCPort          int
	InternalGRPCPort     int
	ApplicationPort      int
	APIListenAddresses   []string
	ApplicationProtocol  Protocol
	Mode                 modes.DaprMode
	PlacementAddresses   []string
	GlobalConfig         string
	AllowedOrigins       string
	Standalone           config.StandaloneConfig
	Kubernetes           config.KubernetesConfig
	MaxConcurrency       int
	mtlsEnabled          bool
	SentryServiceAddress string
	CertChain            *credentials.CertChain
	AppSSL               bool
	MaxRequestBodySize   int
	UnixDomainSocket     string
	ReadBufferSize       int
	StreamRequestBody    bool
}

// NewRuntimeConfig returns a new runtime config.
func NewRuntimeConfig(
	id string, placementAddresses []string,
	controlPlaneAddress, allowedOrigins, globalConfig, componentsPath, appProtocol, mode string,
	httpPort, internalGRPCPort, apiGRPCPort int, apiListenAddresses []string, publicPort *int, appPort, profilePort int,
	enableProfiling bool, maxConcurrency int, mtlsEnabled bool, sentryAddress string, appSSL bool, maxRequestBodySize int, unixDomainSocket string, readBufferSize int, streamRequestBody bool) *Config {
	return &Config{
		ID:                  id,
		HTTPPort:            httpPort,
		PublicPort:          publicPort,
		InternalGRPCPort:    internalGRPCPort,
		APIGRPCPort:         apiGRPCPort,
		ApplicationPort:     appPort,
		ProfilePort:         profilePort,
		APIListenAddresses:  apiListenAddresses,
		ApplicationProtocol: Protocol(appProtocol),
		Mode:                modes.DaprMode(mode),
		PlacementAddresses:  placementAddresses,
		GlobalConfig:        globalConfig,
		AllowedOrigins:      allowedOrigins,
		Standalone: config.StandaloneConfig{
			ComponentsPath: componentsPath,
		},
		Kubernetes: config.KubernetesConfig{
			ControlPlaneAddress: controlPlaneAddress,
		},
		EnableProfiling:      enableProfiling,
		MaxConcurrency:       maxConcurrency,
		mtlsEnabled:          mtlsEnabled,
		SentryServiceAddress: sentryAddress,
		AppSSL:               appSSL,
		MaxRequestBodySize:   maxRequestBodySize,
		UnixDomainSocket:     unixDomainSocket,
		ReadBufferSize:       readBufferSize,
		StreamRequestBody:    streamRequestBody,
	}
}
