package http

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestInit(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"url": "a", "method": "a"}
	hs := HTTPSource{}
	err := hs.Init(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", hs.metadata.URL)
	assert.Equal(t, "a", hs.metadata.Method)

}
