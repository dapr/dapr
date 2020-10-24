// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"time"

	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/modes"
)

// Protocol is a communications protocol
type Protocol string

const (
	// GRPCProtocol is a gRPC communication protocol
	GRPCProtocol Protocol = "grpc"
	// HTTPProtocol is a HTTP communication protocol
	HTTPProtocol Protocol = "http"
	// DefaultDaprHTTPPort is the default http port for Dapr
	DefaultDaprHTTPPort = 3500
	// DefaultDaprAPIGRPCPort is the default API gRPC port for Dapr
	DefaultDaprAPIGRPCPort = 50001
	// DefaultProfilePort is the default port for profiling endpoints
	DefaultProfilePort = 7777
	// DefaultMetricsPort is the default port for metrics endpoints
	DefaultMetricsPort = 9090
	// DefaultMaxRequestBodySize is the default option for the maximum body size in MB for Dapr HTTP servers
	DefaultMaxRequestBodySize = 4
)

// Config holds the Dapr Runtime configuration
type Config struct {
	ID                   string
	HTTPPort             int
	ProfilePort          int
	EnableProfiling      bool
	APIGRPCPort          int
	InternalGRPCPort     int
	ApplicationPort      int
	ApplicationProtocol  Protocol
	Mode                 modes.DaprMode
	PlacementAddresses   []string
	GlobalConfig         string
	AllowedOrigins       string
	Standalone           config.StandaloneConfig
	Kubernetes           config.KubernetesConfig
	MaxConcurrency       int
	ChannelTimeout       time.Duration
	mtlsEnabled          bool
	SentryServiceAddress string
	CertChain            *credentials.CertChain
	AppSSL               bool
	MaxRequestBodySize   int
}

// NewRuntimeConfig returns a new runtime config
func NewRuntimeConfig(
	id string, placementAddresses []string,
	controlPlaneAddress, allowedOrigins, globalConfig, componentsPath, appProtocol, mode string,
	httpPort, internalGRPCPort, apiGRPCPort, appPort, profilePort int,
	enableProfiling bool, maxConcurrency int, timeout time.Duration, mtlsEnabled bool, sentryAddress string, appSSL bool, maxRequestBodySize int) *Config {
	return &Config{
		ID:                  id,
		HTTPPort:            httpPort,
		InternalGRPCPort:    internalGRPCPort,
		APIGRPCPort:         apiGRPCPort,
		ApplicationPort:     appPort,
		ProfilePort:         profilePort,
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
		ChannelTimeout:       timeout,
		mtlsEnabled:          mtlsEnabled,
		SentryServiceAddress: sentryAddress,
		AppSSL:               appSSL,
		MaxRequestBodySize:   maxRequestBodySize,
	}
}
