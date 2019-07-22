package runtime

import (
	config "github.com/actionscore/actions/pkg/config/modes"
	"github.com/actionscore/actions/pkg/modes"
)

type Protocol string

const (
	GRPCProtocol Protocol = "grpc"
	HTTPProtocol Protocol = "http"

	DefaultActionsHTTPPort = 3500
	DefaultActionsGRPCPort = 50001
	DefaultComponentsPath  = "./components"
	DefaultAllowedOrigins  = "*"
)

type RuntimeConfig struct {
	ID                      string
	HTTPPort                int
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

func NewRuntimeConfig(id, placementServiceAddress, controlPlaneAddress, allowedOrigins, globalConfig, componentsPath, appProtocol, mode string, httpPort, grpcPort, appPort int) *RuntimeConfig {
	return &RuntimeConfig{
		ID:                      id,
		HTTPPort:                httpPort,
		GRPCPort:                grpcPort,
		ApplicationPort:         appPort,
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
