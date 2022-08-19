package configuration

import (
	"testing"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"

	"github.com/stretchr/testify/assert"
)

func TestMustLoadConfigurationStore(t *testing.T) {
	l := pluggable.MustLoad[Configuration](pluggable.Component{
		Type: string(components.Configuration),
	})
	assert.NotNil(t, l)
}
