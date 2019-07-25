package pubsub

import (
	"testing"

	cpubsub "github.com/actionscore/actions/pkg/components/pubsub"
	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	testRegistry := cpubsub.NewPubSubRegsitry()
	Load()

	t.Run("redis is registered", func(t *testing.T) {
		p, e := testRegistry.CreatePubSub("pubsub.redis")
		assert.NotNil(t, p)
		assert.Nil(t, e)
	})

	// TODO : add kafka registry test
}
