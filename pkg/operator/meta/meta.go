package meta

import (
	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/kit/utils"
)

// IsAnnotatedForDapr whether the dapr enabled annotation is present and true.
func IsAnnotatedForDapr(a map[string]string) bool {
	return utils.IsTruthy(a[annotations.KeyEnabled])
}

// IsSidecarPresent whether the daprd sidecar is present, either because injector added it or because the user did.
func IsSidecarPresent(labels map[string]string) bool {
	if _, ok := labels[injectorConsts.SidecarInjectedLabel]; ok {
		return true
	}
	if _, ok := labels[WatchdogPatchedLabel]; ok {
		return true
	}
	return false
}
