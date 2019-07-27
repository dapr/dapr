package pubsub

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateFullName(t *testing.T) {
	t.Run("create redis pubsub key name", func(t *testing.T) {
		assert.Equal(t, "pubsub.redis", createFullName("redis"))
	})

	t.Run("create kafka pubsub key name", func(t *testing.T) {
		assert.Equal(t, "pubsub.kafka", createFullName("kafka"))
	})
}

func TestNewPubSubRegsitry(t *testing.T) {
	registry0 := NewPubSubRegsitry()
	registry1 := NewPubSubRegsitry()

	assert.Equal(t, registry0, registry1, "should be the same object")
}

func TestCreatePubSub(t *testing.T) {
	testRegistry := NewPubSubRegsitry()

	t.Run("pubsub messagebus is registered", func(t *testing.T) {
		const PubSubName = "mockPubSub"
		// Initiate mock object
		mockPubSub := new(MockPubSub)

		// act
		RegisterMessageBus(PubSubName, mockPubSub)
		p, e := testRegistry.CreatePubSub(createFullName(PubSubName))

		// assert
		assert.Equal(t, mockPubSub, p)
		assert.Nil(t, e)
	})

	t.Run("pubsub messagebus is not registed", func(t *testing.T) {
		const PubSubName = "fakeBus"

		// act
		p, e := testRegistry.CreatePubSub(createFullName(PubSubName))

		// assert
		assert.Nil(t, p)
		assert.Equal(t, fmt.Errorf("couldn't find message bus %s", createFullName(PubSubName)), e)
	})
}
