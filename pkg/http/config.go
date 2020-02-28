// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

// ServerConfig holds config values for an HTTP server
type ServerConfig struct {
	AllowedOrigins  string
	AppID           string
	HostAddress     string
	Port            int
	ProfilePort     int
	EnableProfiling bool
	MetricsPort     int
	EnableMetrics   bool
}

// NewServerConfig returns a new HTTP server config
func NewServerConfig(appID string, hostAddress string, port int, profilePort int, allowedOrigins string, enableProfiling bool, metricsPort int, enableMetrics bool) ServerConfig {
	return ServerConfig{
		AllowedOrigins:  allowedOrigins,
		AppID:           appID,
		HostAddress:     hostAddress,
		Port:            port,
		ProfilePort:     profilePort,
		EnableProfiling: enableProfiling,
		MetricsPort:     metricsPort,
		EnableMetrics:   enableMetrics,
	}
}
