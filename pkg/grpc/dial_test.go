package grpc

import (
	"testing"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/stretchr/testify/assert"
)

func TestGetDialAddress(t *testing.T) {
	t.Run("kubernetes mode", func(t *testing.T) {
		m := GetDialAddressPrefix(modes.KubernetesMode)
		assert.Equal(t, "dns:///", m)
	})

	t.Run("self hosted mode", func(t *testing.T) {
		m := GetDialAddressPrefix(modes.StandaloneMode)
		assert.Equal(t, "", m)
	})
}
