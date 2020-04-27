package grpc

import "github.com/dapr/dapr/pkg/modes"

func GetDialAddressPrefix(mode modes.DaprMode) string {
	switch mode {
	case modes.KubernetesMode:
		return "dns:///"
	default:
		return ""
	}
}
