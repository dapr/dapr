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

package configuration_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	c "github.com/dapr/components-contrib/configuration"
	"github.com/dapr/dapr/pkg/components/configuration"
	"github.com/dapr/kit/logger"
)

type mockState struct {
	c.Store
}

func TestRegistry(t *testing.T) {
	testRegistry := configuration.NewRegistry()

	t.Run("configuration is registered", func(t *testing.T) {
		const (
			stateName     = "mockState"
			stateNameV2   = "mockState/v2"
			componentName = "configuration." + stateName
		)

		// Initiate mock object
		mock := &mockState{}
		mockV2 := &mockState{}

		// act
		testRegistry.RegisterComponent(func(_ logger.Logger) c.Store {
			return mock
		}, stateName)
		testRegistry.RegisterComponent(func(_ logger.Logger) c.Store {
			return mockV2
		}, stateNameV2)

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

	t.Run("configuration is not registered", func(t *testing.T) {
		const (
			configurationName = "fakeConfiguration"
			componentName     = "configuration." + configurationName
		)

		// act
		p, actualError := testRegistry.Create(componentName, "v1", "")
		expectedError := fmt.Errorf("couldn't find configuration store %s/v1", componentName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
