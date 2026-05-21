/*
Copyright 2026 The Dapr Authors
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

package vector_test

import (
	"fmt"
	"strings"
	"testing"

	contribvector "github.com/dapr/components-contrib/vector"
	"github.com/dapr/dapr/pkg/components/vector"
	"github.com/dapr/kit/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockVector struct {
	contribvector.Vector
}

func TestRegistry(t *testing.T) {
	t.Parallel()

	t.Run("new registry", func(t *testing.T) {
		t.Parallel()

		registry := vector.NewRegistry()
		require.NotNil(t, registry)
		require.NotNil(t, registry.Logger)
	})

	t.Run("vector is registered", func(t *testing.T) {
		t.Parallel()

		const (
			vectorName    = "mockVector"
			vectorNameV2  = "mockVector/v2"
			componentName = "vector." + vectorName
		)

		registry := vector.NewRegistry()
		mock := &mockVector{}
		mockV2 := &mockVector{}

		registry.RegisterComponent(func(_ logger.Logger) contribvector.Vector {
			return mock
		}, vectorName)
		registry.RegisterComponent(func(_ logger.Logger) contribvector.Vector {
			return mockV2
		}, vectorNameV2)

		p, err := registry.Create(componentName, "v0", "")
		require.NoError(t, err)
		assert.Same(t, mock, p)
		p, err = registry.Create(componentName, "v1", "")
		require.NoError(t, err)
		assert.Same(t, mock, p)

		pV2, err := registry.Create(componentName, "v2", "")
		require.NoError(t, err)
		assert.Same(t, mockV2, pV2)

		pV2, err = registry.Create(strings.ToUpper(componentName), "V2", "")
		require.NoError(t, err)
		assert.Same(t, mockV2, pV2)

		p, err = registry.Create(componentName, "v3", "")
		require.Error(t, err)
		assert.Nil(t, p)
	})

	t.Run("vector is not registered", func(t *testing.T) {
		t.Parallel()

		const (
			vectorName    = "fakeVector"
			componentName = "vector." + vectorName
		)

		registry := vector.NewRegistry()
		p, actualError := registry.Create(componentName, "v1", "")
		expectedError := fmt.Errorf("couldn't find vector %s/v1", componentName)

		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
