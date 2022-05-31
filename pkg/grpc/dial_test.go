package grpc

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/modes"
)

func TestGetDialAddress(t *testing.T) {
	t.Run("kubernetes mode", func(t *testing.T) {
		m := GetDialAddressPrefix(modes.KubernetesMode)
		if runtime.GOOS != "windows" {
			assert.Equal(t, "dns:///", m)
		} else {
			assert.Equal(t, "", m)
		}
	})

	t.Run("self hosted mode", func(t *testing.T) {
		m := GetDialAddressPrefix(modes.StandaloneMode)
		assert.Equal(t, "", m)
	})
}
