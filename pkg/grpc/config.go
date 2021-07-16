// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

// ServerConfig is the config object for a grpc server.
type ServerConfig struct {
	AppID              string
	HostAddress        string
	Port               int
	NameSpace          string
	TrustDomain        string
	MaxRequestBodySize int
	APIListenAddress   string
}

// NewServerConfig returns a new grpc server config.
func NewServerConfig(appID string, hostAddress string, port int, namespace string, trustDomain string, maxRequestBodySize int, apiListenAddress string) ServerConfig {
	return ServerConfig{
		AppID:              appID,
		HostAddress:        hostAddress,
		Port:               port,
		NameSpace:          namespace,
		TrustDomain:        trustDomain,
		MaxRequestBodySize: maxRequestBodySize,
		APIListenAddress:   apiListenAddress,
	}
}
