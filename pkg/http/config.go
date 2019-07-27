package http

// ServerConfig holds config values for an HTTP server
type ServerConfig struct {
	AllowedOrigins string
	Port           int
}

// NewServerConfig returns a new HTTP server config
func NewServerConfig(port int, allowedOrigins string) ServerConfig {
	return ServerConfig{
		AllowedOrigins: allowedOrigins,
		Port:           port,
	}
}
