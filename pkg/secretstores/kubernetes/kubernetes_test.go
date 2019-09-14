package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetNamespace(t *testing.T) {
	t.Run("has namespace", func(t *testing.T) {
		store := kubernetesSecretStore{}
		namespace := "a"

		ns, err := store.getNamespaceFromMetadata(map[string]string{"namespace": namespace})
		assert.Nil(t, err)
		assert.Equal(t, namespace, ns)
	})

	t.Run("no namespace", func(t *testing.T) {
		store := kubernetesSecretStore{}
		_, err := store.getNamespaceFromMetadata(map[string]string{})

		assert.NotNil(t, err)
		assert.Equal(t, "namespace is missing on metadata", err.Error())
	})
}
