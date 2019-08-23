package http

// ServerConfig holds config values for an HTTP server
type ServerConfig struct {
	AllowedOrigins string
	ActionID       string
	Port           int
}

// NewServerConfig returns a new HTTP server config
func NewServerConfig(actionID string, port int, allowedOrigins string) ServerConfig {
	return ServerConfig{
		AllowedOrigins: allowedOrigins,
		ActionID:       actionID,
		Port:           port,
	}
}
