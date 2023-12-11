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

package pubsub

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func TestCreateFullName(t *testing.T) {
	t.Run("create redis pubsub key name", func(t *testing.T) {
		assert.Equal(t, "pubsub.redis", createFullName("redis"))
	})

	t.Run("create kafka pubsub key name", func(t *testing.T) {
		assert.Equal(t, "pubsub.kafka", createFullName("kafka"))
	})

	t.Run("create azure service bus pubsub key name", func(t *testing.T) {
		assert.Equal(t, "pubsub.azure.servicebus", createFullName("azure.servicebus"))
	})

	t.Run("create rabbitmq pubsub key name", func(t *testing.T) {
		assert.Equal(t, "pubsub.rabbitmq", createFullName("rabbitmq"))
	})
}

func TestCreatePubSub(t *testing.T) {
	testRegistry := NewRegistry()

	t.Run("pubsub messagebus is registered", func(t *testing.T) {
		const (
			pubSubName    = "mockPubSub"
			pubSubNameV2  = "mockPubSub/v2"
			componentName = "pubsub." + pubSubName
		)

		// Initiate mock object
		mockPubSub := new(daprt.MockPubSub)
		mockPubSubV2 := new(daprt.MockPubSub)

		// act
		testRegistry.RegisterComponent(func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		}, pubSubName)
		testRegistry.RegisterComponent(func(_ logger.Logger) pubsub.PubSub {
			return mockPubSubV2
		}, pubSubNameV2)

		// assert v0 and v1
		p, e := testRegistry.Create(componentName, "v0", "")
		require.NoError(t, e)
		assert.Same(t, mockPubSub, p)

		p, e = testRegistry.Create(componentName, "v1", "")
		require.NoError(t, e)
		assert.Same(t, mockPubSub, p)

		// assert v2
		pV2, e := testRegistry.Create(componentName, "v2", "")
		require.NoError(t, e)
		assert.Same(t, mockPubSubV2, pV2)

		// check case-insensitivity
		pV2, e = testRegistry.Create(strings.ToUpper(componentName), "V2", "")
		require.NoError(t, e)
		assert.Same(t, mockPubSubV2, pV2)
	})

	t.Run("pubsub messagebus is not registered", func(t *testing.T) {
		const PubSubName = "fakePubSub"

		// act
		p, actualError := testRegistry.Create(createFullName(PubSubName), "v1", "")
		expectedError := fmt.Errorf("couldn't find message bus %s/v1", createFullName(PubSubName))
		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
