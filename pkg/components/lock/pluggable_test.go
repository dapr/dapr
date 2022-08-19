package lock

import (
	"testing"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"

	"github.com/stretchr/testify/assert"
)

func TestMustLoadLock(t *testing.T) {
	l := pluggable.MustLoad[Lock](pluggable.Component{
		Type: string(components.Lock),
	})
	assert.NotNil(t, l)
}
