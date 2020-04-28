package grpc

import "github.com/dapr/dapr/pkg/modes"

// GetDialAddressPrefix returns a dial prefix for a gRPC client connections
// For a given DaprMode.
func GetDialAddressPrefix(mode modes.DaprMode) string {
	switch mode {
	case modes.KubernetesMode:
		return "dns:///"
	default:
		return ""
	}
}
