// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

// ServerConfig holds config values for an HTTP server.
type ServerConfig struct {
	AllowedOrigins     string
	AppID              string
	HostAddress        string
	Port               int
	APIListenAddresses []string
	PublicPort         *int
	ProfilePort        int
	EnableProfiling    bool
	MaxRequestBodySize int
	UnixDomainSocket   string
	ReadBufferSize     int
	StreamRequestBody  bool
}

// NewServerConfig returns a new HTTP server config.
func NewServerConfig(appID string, hostAddress string, port int, apiListenAddresses []string, publicPort *int, profilePort int, allowedOrigins string, enableProfiling bool, maxRequestBodySize int, unixDomainSocket string, readBufferSize int, streamRequestBody bool) ServerConfig {
	return ServerConfig{
		AllowedOrigins:     allowedOrigins,
		AppID:              appID,
		HostAddress:        hostAddress,
		Port:               port,
		APIListenAddresses: apiListenAddresses,
		PublicPort:         publicPort,
		ProfilePort:        profilePort,
		EnableProfiling:    enableProfiling,
		MaxRequestBodySize: maxRequestBodySize,
		UnixDomainSocket:   unixDomainSocket,
		ReadBufferSize:     readBufferSize,
		StreamRequestBody:  streamRequestBody,
	}
}
