package grpc

// ServerConfig is the config object for a grpc server
type ServerConfig struct {
	Port int
}

// NewServerConfig returns a new grpc server config
func NewServerConfig(port int) ServerConfig {
	return ServerConfig{
		Port: port,
	}
}
