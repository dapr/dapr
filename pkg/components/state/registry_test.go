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
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	s "github.com/dapr/components-contrib/state"

	"github.com/dapr/dapr/pkg/components/state"
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

		// act
		testRegistry.Register(state.New(stateName, func() s.Store {
			return mock
		}))
		testRegistry.Register(state.New(stateNameV2, func() s.Store {
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

	t.Run("state is not registered", func(t *testing.T) {
		const (
			stateName     = "fakeState"
			componentName = "state." + stateName
		)

		// act
		p, actualError := testRegistry.Create(componentName, "v1")
		expectedError := errors.Errorf("couldn't find state store %s/v1", componentName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
