package pubsub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	testRegistry := NewPubSubRegsitry()
	Load()

	t.Run("redis is registered", func(t *testing.T) {
		p, e := testRegistry.CreatePubSub("pubsub.redis")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})

	// TODO : add kafka registry test
}
