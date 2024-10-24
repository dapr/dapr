package meta

import (
	"strconv"

	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/kit/utils"
)

// IsAnnotatedForDapr whether the dapr enabled annotation is present and true.
func IsAnnotatedForDapr(a map[string]string) bool {
	return utils.IsTruthy(a[annotations.KeyEnabled])
}

func GetAnnotationValueOrDefault(a map[string]string, annotationKey string, defaultValue int) int {
	// return value of annotation if exists, otherwise return default value
	if value, ok := a[annotationKey]; ok {
		val, err := strconv.Atoi(value)

		if err == nil {
			return val
		}
	}
	return defaultValue
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
