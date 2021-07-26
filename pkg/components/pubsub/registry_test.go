// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/components-contrib/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
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
		testRegistry.Register(New(pubSubName, func() pubsub.PubSub {
			return mockPubSub
		}))
		testRegistry.Register(New(pubSubNameV2, func() pubsub.PubSub {
			return mockPubSubV2
		}))

		// assert v0 and v1
		p, e := testRegistry.Create(componentName, "v0")
		assert.NoError(t, e)
		assert.Same(t, mockPubSub, p)

		p, e = testRegistry.Create(componentName, "v1")
		assert.NoError(t, e)
		assert.Same(t, mockPubSub, p)

		// assert v2
		pV2, e := testRegistry.Create(componentName, "v2")
		assert.NoError(t, e)
		assert.Same(t, mockPubSubV2, pV2)

		// check case-insensitivity
		pV2, e = testRegistry.Create(strings.ToUpper(componentName), "V2")
		assert.NoError(t, e)
		assert.Same(t, mockPubSubV2, pV2)
	})

	t.Run("pubsub messagebus is not registered", func(t *testing.T) {
		const PubSubName = "fakePubSub"

		// act
		p, actualError := testRegistry.Create(createFullName(PubSubName), "v1")
		expectedError := errors.Errorf("couldn't find message bus %s/v1", createFullName(PubSubName))
		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
