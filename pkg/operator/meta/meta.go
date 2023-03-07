package meta

import (
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"
	"github.com/dapr/dapr/utils"
)

// IsAnnotatedForDapr whether the dapr enabled annotation is present and true
func IsAnnotatedForDapr(a map[string]string) bool {
	if v, ok := a[annotations.KeyEnabled]; !ok {
		return false
	} else {
		return utils.IsTruthy(v)
	}
}

// IsSidecarPresent whether the daprd sidecar is present, either because injector added it or because the user did
func IsSidecarPresent(labels map[string]string) bool {
	if _, ok := labels[sidecar.SidecarInjectedLabel]; ok {
		return true
	}
	if _, ok := labels[WatchdogPatchedLabel]; ok {
		return true
	}
	return false
}
