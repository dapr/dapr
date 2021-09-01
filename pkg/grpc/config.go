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
	APIListenAddress   string
	NameSpace          string
	TrustDomain        string
	MaxRequestBodySize int
	EnableDomainSocket bool
}

// NewServerConfig returns a new grpc server config.
func NewServerConfig(appID string, hostAddress string, port int, apiListenAddress string, namespace string, trustDomain string, maxRequestBodySize int, enableDomainSocket bool) ServerConfig {
	return ServerConfig{
		AppID:              appID,
		HostAddress:        hostAddress,
		Port:               port,
		APIListenAddress:   apiListenAddress,
		NameSpace:          namespace,
		TrustDomain:        trustDomain,
		MaxRequestBodySize: maxRequestBodySize,
		EnableDomainSocket: enableDomainSocket,
	}
}
