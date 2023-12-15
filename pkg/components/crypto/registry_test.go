/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package crypto_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cp "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/components/crypto"
	"github.com/dapr/kit/logger"
)

type mockCryptoProvider struct {
	cp.SubtleCrypto
}

func TestRegistry(t *testing.T) {
	testRegistry := crypto.NewRegistry()

	t.Run("cryto provider is registered", func(t *testing.T) {
		const (
			cryptoProviderName   = "mockCrypto"
			cryptoProviderNameV2 = "mockCrypto/v2"
			componentName        = "crypto." + cryptoProviderName
		)

		// Initiate mock object
		mock := &mockCryptoProvider{}
		mockV2 := &mockCryptoProvider{}

		// act
		testRegistry.RegisterComponent(func(_ logger.Logger) cp.SubtleCrypto {
			return mock
		}, cryptoProviderName)
		testRegistry.RegisterComponent(func(_ logger.Logger) cp.SubtleCrypto {
			return mockV2
		}, cryptoProviderNameV2)

		// assert v0 and v1
		p, e := testRegistry.Create(componentName, "v0", "")
		require.NoError(t, e)
		assert.Same(t, mock, p)
		p, e = testRegistry.Create(componentName, "v1", "")
		require.NoError(t, e)
		assert.Same(t, mock, p)

		// assert v2
		pV2, e := testRegistry.Create(componentName, "v2", "")
		require.NoError(t, e)
		assert.Same(t, mockV2, pV2)

		// check case-insensitivity
		pV2, e = testRegistry.Create(strings.ToUpper(componentName), "V2", "")
		require.NoError(t, e)
		assert.Same(t, mockV2, pV2)
	})

	t.Run("crypto provider is not registered", func(t *testing.T) {
		const (
			resolverName  = "fakeCrypto"
			componentName = "crypto." + resolverName
		)

		// act
		p, actualError := testRegistry.Create(componentName, "v1", "")
		expectedError := fmt.Errorf("couldn't find crypto provider %s/v1", componentName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
