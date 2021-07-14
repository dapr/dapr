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
	APIListenAddress   string
	PublicPort         *int
	ProfilePort        int
	EnableProfiling    bool
	MaxRequestBodySize int
	EnableDomainSocket bool
}

// NewServerConfig returns a new HTTP server config.
func NewServerConfig(appID string, hostAddress string, port int, apiListenAddress string, publicPort *int, profilePort int, allowedOrigins string, enableProfiling bool, maxRequestBodySize int, enableDomainSocket bool) ServerConfig {
	return ServerConfig{
		AllowedOrigins:     allowedOrigins,
		AppID:              appID,
		HostAddress:        hostAddress,
		Port:               port,
		APIListenAddress:   apiListenAddress,
		PublicPort:         publicPort,
		ProfilePort:        profilePort,
		EnableProfiling:    enableProfiling,
		MaxRequestBodySize: maxRequestBodySize,
		EnableDomainSocket: enableDomainSocket,
	}
}
