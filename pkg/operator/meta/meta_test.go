package meta

import (
	"testing"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
)

func TestIsSidecarPresent(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{
			name: "notPresentLabelsEmpty",
			want: false,
		},
		{
			name:   "notPresent",
			labels: map[string]string{"app": "my-app"},
			want:   false,
		},
		{
			name:   "presentInjected",
			labels: map[string]string{injectorConsts.SidecarInjectedLabel: "yes"},
			want:   true,
		},
		{
			name:   "presentPatched",
			labels: map[string]string{WatchdogPatchedLabel: "yes"},
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSidecarPresent(tt.labels); got != tt.want {
				t.Errorf("IsSidecarPresent() = %v, want %v", got, tt.want)
			}
		})
	}
}
