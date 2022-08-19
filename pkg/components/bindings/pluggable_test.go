package bindings

import (
	"testing"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"

	"github.com/stretchr/testify/assert"
)

func TestMustLoadInputBinding(t *testing.T) {
	l := pluggable.MustLoad[InputBinding](pluggable.Component{
		Type: string(components.InputBinding),
	})
	assert.NotNil(t, l)
}

func TestMustLoadOutputBinding(t *testing.T) {
	l := pluggable.MustLoad[OutputBinding](pluggable.Component{
		Type: string(components.OutputBinding),
	})
	assert.NotNil(t, l)
}
