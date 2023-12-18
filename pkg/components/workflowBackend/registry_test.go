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

package workflowBackend_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	wfbe "github.com/dapr/components-contrib/wfbackend"
	wbe "github.com/dapr/dapr/pkg/components/workflowBackend"
	"github.com/dapr/kit/logger"
)

type mockWorkflowBackend struct {
	wfbe.WorkflowBackend
}

func TestRegistry(t *testing.T) {
	testRegistry := wbe.NewRegistry()

	t.Run("workflow backend is registered", func(t *testing.T) {
		const (
			backendName   = "testbackendname"
			backendNameV2 = "testbackendname/v2"
			componentType = "workflowbackend." + backendName
		)

		// Initiate mock object
		wbeMock := &mockWorkflowBackend{}
		wbeMockV2 := &mockWorkflowBackend{}

		// act
		testRegistry.RegisterComponent(func(_ logger.Logger) wfbe.WorkflowBackend {
			return wbeMock
		}, backendName)
		testRegistry.RegisterComponent(func(_ logger.Logger) wfbe.WorkflowBackend {
			return wbeMockV2
		}, backendNameV2)

		// assert v0 and v1
		wbe, e := testRegistry.Create(componentType, "v0", "")
		require.NoError(t, e)
		assert.Same(t, wbeMock, wbe)
		wbe, e = testRegistry.Create(componentType, "v1", "")
		require.NoError(t, e)
		assert.Same(t, wbeMock, wbe)

		// assert v2
		wbeV2, e := testRegistry.Create(componentType, "v2", "")
		require.NoError(t, e)
		assert.Same(t, wbeMockV2, wbeV2)

		// check case-insensitivity
		wbeV2, e = testRegistry.Create(strings.ToUpper(componentType), "V2", "")
		require.NoError(t, e)
		assert.Same(t, wbeMockV2, wbeV2)

		// check case-insensitivity
		testRegistry.Logger = logger.NewLogger("wfengine.backend")
		wbeV2, e = testRegistry.Create(strings.ToUpper(componentType), "V2", "workflowbackendlog")
		require.NoError(t, e)
		assert.Same(t, wbeMockV2, wbeV2)
	})

	t.Run("workflow backend is not registered", func(t *testing.T) {
		const (
			backendName   = "fakeBackend"
			componentType = "workflowbackend." + backendName
		)

		// act
		wbe, actualError := testRegistry.Create(componentType, "v1", "")
		expectedError := fmt.Errorf("couldn't find wokflow backend %s/v1", componentType)

		// assert
		assert.Nil(t, wbe)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
