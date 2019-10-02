package grpc

// ServerConfig is the config object for a grpc server
type ServerConfig struct {
	DaprID    string
	HostAddress string
	Port        int
}

// NewServerConfig returns a new grpc server config
func NewServerConfig(daprID string, hostAddress string, port int) ServerConfig {
	return ServerConfig{
		DaprID:    daprID,
		HostAddress: hostAddress,
		Port:        port,
	}
}
