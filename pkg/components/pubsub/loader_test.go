// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	testRegistry := NewRegistry()
	Load()

	t.Run("redis is registered", func(t *testing.T) {
		p, e := testRegistry.CreatePubSub("pubsub.redis")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})

	// TODO : add kafka registry test

	t.Run("azure servicebus is registered", func(t *testing.T) {
		p, e := testRegistry.CreatePubSub("pubsub.azure.servicebus")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})

	t.Run("rabbitmq is registered", func(t *testing.T) {
		p, e := testRegistry.CreatePubSub("pubsub.rabbitmq")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})
}
