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
	APIListenAddresses []string
	NameSpace          string
	TrustDomain        string
	MaxRequestBodySize int
	UnixDomainSocket   string
	ReadBufferSize     int
}

// NewServerConfig returns a new grpc server config.
func NewServerConfig(appID string, hostAddress string, port int, apiListenAddresses []string, namespace string, trustDomain string, maxRequestBodySize int, unixDomainSocket string, readBufferSize int) ServerConfig {
	return ServerConfig{
		AppID:              appID,
		HostAddress:        hostAddress,
		Port:               port,
		APIListenAddresses: apiListenAddresses,
		NameSpace:          namespace,
		TrustDomain:        trustDomain,
		MaxRequestBodySize: maxRequestBodySize,
		UnixDomainSocket:   unixDomainSocket,
		ReadBufferSize:     readBufferSize,
	}
}
