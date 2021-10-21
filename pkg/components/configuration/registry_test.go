// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package configuration_test

import (
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/components/configuration"

	c "github.com/dapr/components-contrib/configuration"
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
		testRegistry.Register(configuration.New(stateName, func() c.Store {
			return mock
		}))
		testRegistry.Register(configuration.New(stateNameV2, func() c.Store {
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

	t.Run("configuration is not registered", func(t *testing.T) {
		const (
			configurationName = "fakeConfiguration"
			componentName     = "configuration." + configurationName
		)

		// act
		p, actualError := testRegistry.Create(componentName, "v1")
		expectedError := errors.Errorf("couldn't find configuration store %s/v1", componentName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
