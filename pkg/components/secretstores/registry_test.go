// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores_test

import (
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	ss "github.com/dapr/components-contrib/secretstores"

	"github.com/dapr/dapr/pkg/components/secretstores"
)

type mockSecretStore struct {
	ss.SecretStore
}

func TestRegistry(t *testing.T) {
	testRegistry := secretstores.NewRegistry()

	t.Run("secret store is registered", func(t *testing.T) {
		const (
			secretStoreName   = "mockSecretStore"
			secretStoreNameV2 = "mockSecretStore/v2"
			componentName     = "secretstores." + secretStoreName
		)

		// Initiate mock object
		mock := &mockSecretStore{}
		mockV2 := &mockSecretStore{}

		// act
		testRegistry.Register(secretstores.New(secretStoreName, func() ss.SecretStore {
			return mock
		}))
		testRegistry.Register(secretstores.New(secretStoreNameV2, func() ss.SecretStore {
			return mockV2
		}))

		// assert v0 and v1
		p, e := testRegistry.Create(componentName, "v0")
		assert.NoError(t, e)
		assert.Same(t, mock, p)
		p, e = testRegistry.Create(componentName, "v1")
		assert.NoError(t, e)
		assert.Same(t, mock, p)

		// assert v2
		pV2, e := testRegistry.Create(componentName, "v2")
		assert.NoError(t, e)
		assert.Same(t, mockV2, pV2)

		// check case-insensitivity
		pV2, e = testRegistry.Create(strings.ToUpper(componentName), "V2")
		assert.NoError(t, e)
		assert.Same(t, mockV2, pV2)
	})

	t.Run("secret store is not registered", func(t *testing.T) {
		const (
			resolverName  = "fakeSecretStore"
			componentName = "secretstores." + resolverName
		)

		// act
		p, actualError := testRegistry.Create(componentName, "v1")
		expectedError := errors.Errorf("couldn't find secret store %s/v1", componentName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
