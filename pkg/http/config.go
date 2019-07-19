package http

type ServerConfig struct {
	AllowedOrigins string
	Port           int
}

func NewServerConfig(port int, allowedOrigins string) ServerConfig {
	return ServerConfig{
		AllowedOrigins: allowedOrigins,
		Port:           port,
	}
}
