// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

// ServerConfig holds config values for an HTTP server
type ServerConfig struct {
	AllowedOrigins  string
	DaprID          string
	HostAddress     string
	Port            int
	ProfilePort     int
	EnableProfiling bool
	MetricsPort     int
	EnableMetrics   bool
}

// NewServerConfig returns a new HTTP server config
func NewServerConfig(daprID string, hostAddress string, port int, profilePort int, allowedOrigins string, enableProfiling bool, metricsPort int, enableMetrics bool) ServerConfig {
	return ServerConfig{
		AllowedOrigins:  allowedOrigins,
		DaprID:          daprID,
		HostAddress:     hostAddress,
		Port:            port,
		ProfilePort:     profilePort,
		EnableProfiling: enableProfiling,
		MetricsPort:     metricsPort,
		EnableMetrics:   enableMetrics,
	}
}
