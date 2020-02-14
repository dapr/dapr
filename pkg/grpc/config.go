// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

// ServerConfig is the config object for a grpc server
type ServerConfig struct {
	DaprID        string
	HostAddress   string
	Port          int
	EnableMetrics bool
}

// NewServerConfig returns a new grpc server config
func NewServerConfig(daprID string, hostAddress string, port int, enableMetrics bool) ServerConfig {
	return ServerConfig{
		DaprID:        daprID,
		HostAddress:   hostAddress,
		Port:          port,
		EnableMetrics: enableMetrics,
	}
}
