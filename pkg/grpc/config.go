package grpc

type ServerConfig struct {
	Port int
}

func NewServerConfig(port int) ServerConfig {
	return ServerConfig{
		Port: port,
	}
}
