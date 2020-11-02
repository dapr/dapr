// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"testing"

	"github.com/dapr/components-contrib/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
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

func TestNewPubSubRegistry(t *testing.T) {
	registry0 := NewRegistry()
	registry1 := NewRegistry()

	assert.Equal(t, registry0, registry1, "should be the same object")
}

func TestCreatePubSub(t *testing.T) {
	testRegistry := NewRegistry()

	t.Run("pubsub messagebus is registered", func(t *testing.T) {
		const PubSubName = "mockPubSub"
		// Initiate mock object
		mockPubSub := new(daprt.MockPubSub)

		// act
		testRegistry.Register(New(PubSubName, func() pubsub.PubSub {
			return mockPubSub
		}))
		p, e := testRegistry.Create(createFullName(PubSubName))

		// assert
		assert.Equal(t, mockPubSub, p)
		assert.Nil(t, e)
	})

	t.Run("pubsub messagebus is not registered", func(t *testing.T) {
		const PubSubName = "fakeBus"

		// act
		p, actualError := testRegistry.Create(createFullName(PubSubName))
		expectedError := errors.Errorf("couldn't find message bus %s", createFullName(PubSubName))
		// assert
		assert.Nil(t, p)
		assert.Equal(t, expectedError.Error(), actualError.Error())
	})
}
