package http

// ServerConfig holds config values for an HTTP server
type ServerConfig struct {
	AllowedOrigins  string
	ActionID        string
	HostAddress     string
	Port            int
	ProfilePort     int
	EnableProfiling bool
}

// NewServerConfig returns a new HTTP server config
func NewServerConfig(actionID string, hostAddress string, port int, profilePort int, allowedOrigins string, enableProfiling bool) ServerConfig {
	return ServerConfig{
		AllowedOrigins:  allowedOrigins,
		ActionID:        actionID,
		HostAddress:     hostAddress,
		Port:            port,
		ProfilePort:     profilePort,
		EnableProfiling: enableProfiling,
	}
}
