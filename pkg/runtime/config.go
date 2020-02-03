// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	config "github.com/dapr/dapr/pkg/config/modes"
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
	// DefaultDaprGRPCPort is the default gRPC port for Dapr
	DefaultDaprGRPCPort = 50001
	// DefaultProfilePort is the default port for profiling endpoints
	DefaultProfilePort = 7777
	// DefaultComponentsPath is the default dir for Dapr components (standalone mode)
	DefaultComponentsPath = "./components"
	// DefaultAllowedOrigins is the default origins allowed for the Dapr HTTP servers
	DefaultAllowedOrigins = "*"
)

// Config holds the Dapr Runtime configuration
type Config struct {
	ID                      string
	HTTPPort                int
	ProfilePort             int
	EnableProfiling         bool
	GRPCPort                int
	ApplicationPort         int
	ApplicationProtocol     Protocol
	Mode                    modes.DaprMode
	PlacementServiceAddress string
	GlobalConfig            string
	AllowedOrigins          string
	Standalone              config.StandaloneConfig
	Kubernetes              config.KubernetesConfig
	MaxConcurrency          int
	mtlsEnabled             bool
	SentryServiceAddress    string
}

// NewRuntimeConfig returns a new runtime config
func NewRuntimeConfig(id, placementServiceAddress, controlPlaneAddress, allowedOrigins, globalConfig, componentsPath, appProtocol, mode string, httpPort, grpcPort, appPort, profilePort int, enableProfiling bool, maxConcurrency int, mtlsEnabled bool, sentryAddress string) *Config {
	return &Config{
		ID:                      id,
		HTTPPort:                httpPort,
		GRPCPort:                grpcPort,
		ApplicationPort:         appPort,
		ProfilePort:             profilePort,
		ApplicationProtocol:     Protocol(appProtocol),
		Mode:                    modes.DaprMode(mode),
		PlacementServiceAddress: placementServiceAddress,
		GlobalConfig:            globalConfig,
		AllowedOrigins:          allowedOrigins,
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
	}
}
