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

package state_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	s "github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/kit/logger"
)

type mockState struct {
	s.Store
}

func TestRegistry(t *testing.T) {
	testRegistry := state.NewRegistry()

	t.Run("state is registered", func(t *testing.T) {
		const (
			stateName     = "mockState"
			stateNameV2   = "mockState/v2"
			componentName = "state." + stateName
		)

		// Initiate mock object
		mock := &mockState{}
		mockV2 := &mockState{}
		fooV1 := new(mockState)
		fooV2 := new(mockState)
		fooV3 := new(mockState)
		fooV4 := new(mockState)
		fooCV1 := func(_ logger.Logger) s.Store { return fooV1 }
		fooCV2 := func(_ logger.Logger) s.Store { return fooV2 }
		fooCV3 := func(_ logger.Logger) s.Store { return fooV3 }
		fooCV4 := func(_ logger.Logger) s.Store { return fooV4 }

		// act
		testRegistry.RegisterComponent(func(_ logger.Logger) s.Store {
			return mock
		}, stateName)
		testRegistry.RegisterComponent(func(_ logger.Logger) s.Store {
			return mockV2
		}, stateNameV2)
		testRegistry.RegisterComponentWithVersions("foo", components.Versioning{
			Preferred: components.VersionConstructor{Version: "v2", Constructor: fooCV2},
			Deprecated: []components.VersionConstructor{
				{Version: "v1", Constructor: fooCV1},
				{Version: "v3", Constructor: fooCV3},
			},
			Others: []components.VersionConstructor{
				{Version: "v4", Constructor: fooCV4},
			},
			Default: "v1",
		})

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

		// Check availability of foo versions

		p, err := testRegistry.Create("state.foo", "v1", "")
		require.NoError(t, err)
		assert.Same(t, fooV1, p)
		p, err = testRegistry.Create("state.foo", "v2", "")
		require.NoError(t, err)
		assert.Same(t, fooV2, p)
		p, err = testRegistry.Create("state.foo", "v3", "")
		require.NoError(t, err)
		assert.Same(t, fooV3, p)
		p, err = testRegistry.Create("state.foo", "v4", "")
		require.NoError(t, err)
		assert.Same(t, fooV4, p)
		p, err = testRegistry.Create("state.foo", "v5", "")
		require.Error(t, err)
		assert.Nil(t, p)
		p, err = testRegistry.Create("state.foo", "", "")
		require.NoError(t, err)
		assert.Same(t, fooV1, p)
		p, err = testRegistry.Create("state.foo", "v0", "")
		require.Error(t, err)
		assert.Nil(t, p)
	})

	t.Run("state is not registered", func(t *testing.T) {
		const (
			stateName     = "fakeState"
			componentName = "state." + stateName
		)

		// act
		p, actualError := testRegistry.Create(componentName, "v1", "")
		expectedError := fmt.Errorf("couldn't find state store %s/v1", componentName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
