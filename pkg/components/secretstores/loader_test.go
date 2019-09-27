package secretstores

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	testRegistry := NewSecretStoreRegistry()
	Load()

	t.Run("kubernetes is registered", func(t *testing.T) {
		p, e := testRegistry.CreateSecretStore("secretstores.kubernetes")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})
}
