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
	ProfilePort        int
	EnableProfiling    bool
	MaxRequestBodySize int
	APIListenAddress   string
}

// NewServerConfig returns a new HTTP server config.
func NewServerConfig(appID string, hostAddress string, port int, profilePort int, allowedOrigins string, enableProfiling bool, maxRequestBodySize int, apiListenAddress string) ServerConfig {
	return ServerConfig{
		AllowedOrigins:     allowedOrigins,
		AppID:              appID,
		HostAddress:        hostAddress,
		Port:               port,
		ProfilePort:        profilePort,
		EnableProfiling:    enableProfiling,
		MaxRequestBodySize: maxRequestBodySize,
		APIListenAddress:   apiListenAddress,
	}
}
