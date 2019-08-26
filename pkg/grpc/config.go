package grpc

// ServerConfig is the config object for a grpc server
type ServerConfig struct {
	ActionID    string
	HostAddress string
	Port        int
}

// NewServerConfig returns a new grpc server config
func NewServerConfig(actionID string, hostAddress string, port int) ServerConfig {
	return ServerConfig{
		ActionID:    actionID,
		HostAddress: hostAddress,
		Port:        port,
	}
}
