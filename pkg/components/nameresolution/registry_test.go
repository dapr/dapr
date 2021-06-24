// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package nameresolution_test

import (
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	nr "github.com/dapr/components-contrib/nameresolution"

	"github.com/dapr/dapr/pkg/components/nameresolution"
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
		testRegistry.Register(nameresolution.New(resolverName, func() nr.Resolver {
			return mock
		}))
		testRegistry.Register(nameresolution.New(resolverNameV2, func() nr.Resolver {
			return mockV2
		}))

		// assert v0 and v1
		p, e := testRegistry.Create(resolverName, "v0")
		assert.NoError(t, e)
		assert.Same(t, mock, p)
		p, e = testRegistry.Create(resolverName, "v1")
		assert.NoError(t, e)
		assert.Same(t, mock, p)

		// assert v2
		pV2, e := testRegistry.Create(resolverName, "v2")
		assert.NoError(t, e)
		assert.Same(t, mockV2, pV2)

		// check case-insensitivity
		pV2, e = testRegistry.Create(strings.ToUpper(resolverName), "V2")
		assert.NoError(t, e)
		assert.Same(t, mockV2, pV2)
	})

	t.Run("name resolver is not registered", func(t *testing.T) {
		const (
			resolverName = "fakeResolver"
		)

		// act
		p, actualError := testRegistry.Create(resolverName, "v1")
		expectedError := errors.Errorf("couldn't find name resolver %s/v1", resolverName)

		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
