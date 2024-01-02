/*
Copyright 2021 The Dapr Authors
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

package nameresolution_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/dapr/pkg/components/nameresolution"
	"github.com/dapr/kit/logger"
)

type mockResolver struct {
	nr.Resolver
}

func TestRegistry(t *testing.T) {
	testRegistry := nameresolution.NewRegistry()

	t.Run("name resolver is registered", func(t *testing.T) {
		const (
			resolverName   = "mockResolver"
			resolverNameV2 = "mockResolver/v2"
		)

		// Initiate mock object
		mock := &mockResolver{}
		mockV2 := &mockResolver{}

		// act
		testRegistry.RegisterComponent(func(_ logger.Logger) nr.Resolver {
			return mock
		}, resolverName)
		testRegistry.RegisterComponent(func(_ logger.Logger) nr.Resolver {
			return mockV2
		}, resolverNameV2)

		// assert v0 and v1
		p, e := testRegistry.Create(resolverName, "v0", "")
		require.NoError(t, e)
		assert.Same(t, mock, p)
		p, e = testRegistry.Create(resolverName, "v1", "")
		require.NoError(t, e)
		assert.Same(t, mock, p)

		// assert v2
		pV2, e := testRegistry.Create(resolverName, "v2", "")
		require.NoError(t, e)
		assert.Same(t, mockV2, pV2)

		// check case-insensitivity
		pV2, e = testRegistry.Create(strings.ToUpper(resolverName), "V2", "")
		require.NoError(t, e)
		assert.Same(t, mockV2, pV2)
	})

	t.Run("name resolver is not registered", func(t *testing.T) {
		const (
			resolverName = "fakeResolver"
		)

		// act
		p, actualError := testRegistry.Create(resolverName, "v1", "")
		expectedError := fmt.Errorf("couldn't find name resolver %s/v1", resolverName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
