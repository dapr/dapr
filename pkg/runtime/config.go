package runtime

import (
	config "github.com/actionscore/actions/pkg/config/modes"
	"github.com/actionscore/actions/pkg/modes"
)

// Protocol is a communications protocol
type Protocol string

const (
	// GRPCProtocol is a gRPC communication protocol
	GRPCProtocol Protocol = "grpc"
	// HTTPProtocol is a HTTP communication protocol
	HTTPProtocol Protocol = "http"
	// DefaultActionsHTTPPort is the default http port for Actions
	DefaultActionsHTTPPort = 3500
	// DefaultActionsGRPCPort is the default gRPC port for Actions
	DefaultActionsGRPCPort = 50001
	// DefaultProfilePort is the default port for profiling endpoints
	DefaultProfilePort = 7777
	// DefaultComponentsPath is the default dir for Actions components (standalone mode)
	DefaultComponentsPath = "./components"
	// DefaultAllowedOrigins is the default origins allowed for the Actions HTTP servers
	DefaultAllowedOrigins = "*"
)

// Config holds the Actions Runtime configuration
type Config struct {
	ID                      string
	HTTPPort                int
	ProfilePort             int
	GRPCPort                int
	ApplicationPort         int
	ApplicationProtocol     Protocol
	Mode                    modes.ActionsMode
	PlacementServiceAddress string
	GlobalConfig            string
	AllowedOrigins          string
	Standalone              config.StandaloneConfig
	Kubernetes              config.KubernetesConfig
}

// NewRuntimeConfig returns a new runtime config
func NewRuntimeConfig(id, placementServiceAddress, controlPlaneAddress, allowedOrigins, globalConfig, componentsPath, appProtocol, mode string, httpPort, grpcPort, appPort, profilePort int) *Config {
	return &Config{
		ID:                      id,
		HTTPPort:                httpPort,
		GRPCPort:                grpcPort,
		ApplicationPort:         appPort,
		ProfilePort:             profilePort,
		ApplicationProtocol:     Protocol(appProtocol),
		Mode:                    modes.ActionsMode(mode),
		PlacementServiceAddress: placementServiceAddress,
		GlobalConfig:            globalConfig,
		AllowedOrigins:          allowedOrigins,
		Standalone: config.StandaloneConfig{
			ComponentsPath: componentsPath,
		},
		Kubernetes: config.KubernetesConfig{
			ControlPlaneAddress: controlPlaneAddress,
		},
	}
}
