// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

// ServerConfig is the config object for a grpc server
type ServerConfig struct {
	AppID       string
	HostAddress string
	Port        int
}

// NewServerConfig returns a new grpc server config
func NewServerConfig(appID string, hostAddress string, port int) ServerConfig {
	return ServerConfig{
		AppID:       appID,
		HostAddress: hostAddress,
		Port:        port,
	}
}
